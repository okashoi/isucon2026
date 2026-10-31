package isureal

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// 画像の JPEG をアプリサーバーのローカルに置く。
// 画像の取得とエクスポートは、バイト列をリクエストのたびに DB から運ばない。
// 画像は ID ごとに中身が変わらないので、取得の可否だけを DB で確認すればよい。
type PostImageStore struct {
	// make post-images/sync が配る初期データの画像。初期化では消さない。
	dir string
	// 実行中に書き込んだ画像と、DB から読んだ画像。初期化で消す。
	runtimeDir string
	mu         sync.Mutex
}

func NewPostImageStore() (*PostImageStore, error) {
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
	return &PostImageStore{dir: dir, runtimeDir: runtimeDir}, nil
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

// Load は画像のバイト列をローカルから返す。無ければ DB から読んで置く。
// 画像の行が無ければ sql.ErrNoRows を返す。取得してよいかは呼び出し側が確認する。
func (s *PostImageStore) Load(ctx context.Context, db sqlx.QueryerContext, id string) ([]byte, error) {
	data, err := s.Read(id)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) && validImageFileID(id) {
		return nil, err
	}
	if err := sqlx.GetContext(ctx, db, &data, `SELECT data FROM image_objects WHERE id = $1`, preparedQuery, id); err != nil {
		return nil, err
	}
	// 置けなくても次の取得で DB から読み直す。
	_ = s.Save(id, data)
	return data, nil
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
		if err := s.loadFromDB(ctx, db, missing[:n], s.dir, nil); err != nil {
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
	if err := s.loadFromDB(ctx, db, missing, s.runtimeDir, out); err != nil {
		return nil, err
	}
	return out, nil
}

// loadFromDB は ids の画像を DB から読んで dir に置く。out が nil でなければ中身も入れる。
func (s *PostImageStore) loadFromDB(ctx context.Context, db *sqlx.DB, ids []string, dir string, out map[string][]byte) error {
	query, args, err := sqlx.In(`SELECT id, data FROM image_objects WHERE id IN (?)`, ids)
	if err != nil {
		return err
	}
	query = sqlx.Rebind(sqlx.DOLLAR, query)
	rows, err := db.QueryxContext(ctx, query, append([]any{preparedQuery}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return err
		}
		s.mu.Lock()
		err := save(dir, id, data)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if out != nil {
			out[id] = data
		}
	}
	return rows.Err()
}

// purgeAllImageCaches は全リージョンのサーバで、実行中に置いた画像を消す。
func purgeAllImageCaches(ctx context.Context, h *HandlerContext) error {
	if err := h.PostImages.Clear(); err != nil {
		return err
	}
	for region, peerURLs := range h.Config.PeerURLs {
		if region == h.Config.Region {
			continue
		}
		for _, peerURL := range peerURLs {
			if err := requestPeerPurge(ctx, peerURL+"/internal/image-cache"); err != nil {
				return err
			}
		}
	}
	// 同一リージョンの他台の runtime キャッシュも消す。
	for _, peerURL := range h.Config.PeerURLs[h.Config.Region] {
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

func fillPostImageCacheHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if err := h.PostImages.FillMissing(c.Request().Context(), h.DB); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
