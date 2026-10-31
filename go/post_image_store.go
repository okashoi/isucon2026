package isureal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// 画像の JPEG をアプリサーバーのローカルに置く。
// 画像の取得とエクスポートは、バイト列をリクエストのたびに DB から運ばない。
// 画像は ID ごとに中身が変わらないので、取得の可否だけを DB で確認すればよい。
//
// 実行中に投稿した画像は DB にバイト列を書かず、書き込んだサーバのローカルにだけ置く（image_objects.origin_region）。
// 同期レプリケーションで画像を WAN 越しに運ばないため。他のサーバはファイルが無ければ書き込んだサーバから取る。
type PostImageStore struct {
	// 初期データの画像。make post-images/sync が配り、DB から読んだものも置く。初期化では消さない。
	dir string
	// 実行中に作られた画像。初期化で消す。
	runtimeDir string
	mu         sync.Mutex
	region     string
	peerURLs   map[string]string
}

// imageInitialColumn は初期化で消えない行を示す列。reset.sql の削除条件の逆。
const imageInitialColumn = `created_at <= (SELECT created_at FROM initial_data_boundary) AS initial`

func NewPostImageStore(region string, peerURLs map[string]string) (*PostImageStore, error) {
	dir := os.Getenv("ISUREAL_POST_IMAGE_DIR")
	if dir == "" {
		// 作業ディレクトリは go/ なので webapp/post-images になる。make post-images/sync もここへ配る。
		dir = "../post-images"
	}
	runtimeDir := os.Getenv("ISUREAL_IMAGE_CACHE_DIR")
	if runtimeDir == "" {
		runtimeDir = "../image-cache"
	}
	for _, d := range []string{dir, runtimeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return &PostImageStore{dir: dir, runtimeDir: runtimeDir, region: region, peerURLs: peerURLs}, nil
}

func imageFilePath(dir, id string) (string, error) {
	if !validImageFileID(id) {
		return "", errors.New("invalid image id")
	}
	return filepath.Join(dir, id), nil
}

func validImageFileID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

func (s *PostImageStore) Read(id string) ([]byte, error) {
	path, err := imageFilePath(s.dir, id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if !errors.Is(err, os.ErrNotExist) {
		return data, err
	}
	return os.ReadFile(filepath.Join(s.runtimeDir, id))
}

// Save は実行中に得た画像を置く。初期化で消える。
func (s *PostImageStore) Save(id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return save(s.runtimeDir, id, data)
}

// saveLoaded は読み出した画像を置く。初期データの画像は次のベンチでも同じ ID で読まれるので、初期化で消さない。
func (s *PostImageStore) saveLoaded(id string, data []byte, initial bool) error {
	dir := s.runtimeDir
	if initial {
		dir = s.dir
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return save(dir, id, data)
}

// SaveOrigin は DB にバイト列を書かない画像を置く。この置き場所が唯一の実体なので、
// 再起動後も残るように fsync してから返す。
func (s *PostImageStore) SaveOrigin(id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := imageFilePath(s.runtimeDir, id)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(s.runtimeDir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Remove は投稿が確定しなかった画像を消す。
func (s *PostImageStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if path, err := imageFilePath(s.runtimeDir, id); err == nil {
		_ = os.Remove(path)
	}
}

func save(dir, id string, data []byte) error {
	path, err := imageFilePath(dir, id)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clear は実行中に置いた画像を消す。初期データの画像は残す。
func (s *PostImageStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.runtimeDir); err != nil {
		return err
	}
	return os.MkdirAll(s.runtimeDir, 0o755)
}

// Load は画像のバイト列をローカルから返す。無ければ、origin があればそのサーバから、無ければ DB から読んで置く。
// origin は image_objects.origin_region。画像の行が無ければ sql.ErrNoRows を返す。取得してよいかは呼び出し側が確認する。
func (s *PostImageStore) Load(ctx context.Context, db sqlx.QueryerContext, id string, origin *string) ([]byte, error) {
	data, err := s.Read(id)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) && validImageFileID(id) {
		return nil, err
	}
	initial := false
	if origin != nil {
		if data, err = s.fetchFromOrigin(ctx, *origin, id); err != nil {
			return nil, err
		}
	} else {
		var image struct {
			Data    []byte `db:"data"`
			Initial bool   `db:"initial"`
		}
		if err := sqlx.GetContext(ctx, db, &image, `SELECT data, `+imageInitialColumn+` FROM image_objects WHERE id = $1`, preparedQuery, id); err != nil {
			return nil, err
		}
		data, initial = image.Data, image.Initial
	}
	// 置けなくても次の取得で読み直す。
	_ = s.saveLoaded(id, data, initial)
	return data, nil
}

// fetchFromOrigin は画像を書き込んだサーバのローカルから取る。
func (s *PostImageStore) fetchFromOrigin(ctx context.Context, region, id string) ([]byte, error) {
	peerURL, ok := s.peerURLs[region]
	if !ok || region == s.region {
		return nil, fmt.Errorf("post image %s is missing on its origin %q", id, region)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, peerURL+"/internal/images/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	res, err := peerClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch post image from %s: %w", region, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch post image %s from %s: %s", id, region, res.Status)
	}
	return io.ReadAll(res.Body)
}

// FillMissing は初期データの画像のうち、ファイルが無いものだけを DB から書き出す。
// 画像は ID ごとに中身が変わらないので、置いたファイルは消さない。
func (s *PostImageStore) FillMissing(ctx context.Context, db *sqlx.DB) error {
	var ids []string
	if err := db.SelectContext(ctx, &ids, `SELECT id FROM image_objects WHERE created_at <= (SELECT created_at FROM initial_data_boundary)`); err != nil {
		return err
	}
	var missing []string
	for _, id := range ids {
		path, err := imageFilePath(s.dir, id)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, id)
	}
	for len(missing) > 0 {
		n := min(len(missing), fillBatchSize)
		if err := s.loadFromDB(ctx, db, missing[:n], nil); err != nil {
			return err
		}
		missing = missing[n:]
	}
	return nil
}

const fillBatchSize = 500

func (s *PostImageStore) Bytes(ctx context.Context, db *sqlx.DB, ids []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(ids))
	var missing []string
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		data, err := s.Read(id)
		if err == nil {
			out[id] = data
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return out, nil
	}
	if err := s.loadFromDB(ctx, db, missing, out); err != nil {
		return nil, err
	}
	return out, nil
}

// loadFromDB は ids の画像を DB から読んで置く。out が nil でなければ中身も入れる。
// DB にバイト列が無い画像（origin_region がある行）は、書き込んだサーバから取る。
func (s *PostImageStore) loadFromDB(ctx context.Context, db *sqlx.DB, ids []string, out map[string][]byte) error {
	query, args, err := sqlx.In(`SELECT id, data, origin_region, `+imageInitialColumn+` FROM image_objects WHERE id IN (?)`, ids)
	if err != nil {
		return err
	}
	query = sqlx.Rebind(sqlx.DOLLAR, query)
	type remoteImage struct{ id, origin string }
	var remote []remoteImage
	rows, err := db.QueryxContext(ctx, query, append([]any{preparedQuery}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var data []byte
		var origin *string
		var initial bool
		if err := rows.Scan(&id, &data, &origin, &initial); err != nil {
			return err
		}
		if origin != nil {
			remote = append(remote, remoteImage{id: id, origin: *origin})
			continue
		}
		if err := s.store(id, data, initial, out); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// 他のサーバへの往復のあいだ DB の接続を持たないように、行を読み終えてから取る。
	rows.Close()
	for _, image := range remote {
		data, err := s.fetchFromOrigin(ctx, image.origin, image.id)
		if err != nil {
			return err
		}
		if err := s.store(image.id, data, false, out); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostImageStore) store(id string, data []byte, initial bool, out map[string][]byte) error {
	if err := s.saveLoaded(id, data, initial); err != nil {
		return err
	}
	if out != nil {
		out[id] = data
	}
	return nil
}

// purgeAllImageCaches は全リージョンのサーバで、実行中に置いた画像を消す。
func purgeAllImageCaches(ctx context.Context, h *HandlerContext) error {
	if err := h.PostImages.Clear(); err != nil {
		return err
	}
	for region, peerURL := range h.Config.PeerURLs {
		if region == h.Config.Region {
			continue
		}
		if err := requestPeerPurge(ctx, peerURL+"/internal/image-cache"); err != nil {
			return err
		}
	}
	return nil
}

func purgeImageCacheHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if err := h.PostImages.Clear(); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// getOriginImageHandler は、このサーバで書き込んだ画像を他のサーバへ返す。取得の可否は取りに来たサーバが確認済み。
func getOriginImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		data, err := h.PostImages.Read(c.Param("imageId"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || !validImageFileID(c.Param("imageId")) {
				return c.NoContent(http.StatusNotFound)
			}
			return err
		}
		return c.Blob(http.StatusOK, "image/jpeg", data)
	}
}

func fillPostImageCacheHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if err := h.PostImages.FillMissing(c.Request().Context(), h.DB); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
