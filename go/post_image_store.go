package isureal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
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
	// 自分以外のリージョンのアプリサーバ。
	peers []string
}

var errImageBodyMissing = errors.New("image body missing on all servers")

func NewPostImageStore(peers []string) (*PostImageStore, error) {
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
	return &PostImageStore{dir: dir, runtimeDir: runtimeDir, peers: peers}, nil
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

// saveIfAbsent は既にあれば何もしない。ID ごとに中身は変わらないので、後から来た配布で上書きしない。
func (s *PostImageStore) saveIfAbsent(id string, data []byte) error {
	path, err := imageFilePath(s.runtimeDir, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return save(s.runtimeDir, id, data)
}

// Replicate は id の本体をこのサーバと全ピアのローカルに置き始める。
// 返す関数は完了を待ち、image_objects.data に入れる値を返す。全台に置けたら空、どこかで失敗したら本体。
// 空の行を commit してよいのは全台に置けたあとだけなので、行を INSERT する前に必ず待つ。
func (s *PostImageStore) Replicate(ctx context.Context, id string, data []byte) (wait func() []byte) {
	ctx = context.WithoutCancel(ctx)
	errs := make(chan error, len(s.peers)+1)
	go func() { errs <- s.Save(id, data) }()
	for _, peer := range s.peers {
		go func() { errs <- putPeerImage(ctx, peer+"/internal/image-blob/"+id, data) }()
	}
	return func() []byte {
		var err error
		for range len(s.peers) + 1 {
			err = errors.Join(err, <-errs)
		}
		if err != nil {
			log.Printf("image replicate failed; storing body in DB: %v", err)
			return data
		}
		return []byte{}
	}
}

func putPeerImage(ctx context.Context, target string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	res, err := peerClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("put peer image: %s: %s", target, res.Status)
	}
	return nil
}

// pullFromPeers は DB の行が空のとき、ピアのローカルにある本体を取ってきて置く。
func (s *PostImageStore) pullFromPeers(ctx context.Context, id string) ([]byte, error) {
	for _, peer := range s.peers {
		data, err := getPeerImage(ctx, peer+"/internal/image-blob/"+id)
		if err != nil || len(data) == 0 {
			continue
		}
		_ = s.saveIfAbsent(id, data)
		return data, nil
	}
	return nil, errImageBodyMissing
}

func getPeerImage(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	res, err := peerClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get peer image: %s: %s", target, res.Status)
	}
	return io.ReadAll(res.Body)
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

// Load は画像のバイト列をローカルから返す。無ければ DB から、DB の data が空ならピアから読んで置く。
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
	if len(data) == 0 {
		return s.pullFromPeers(ctx, id)
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
	for _, id := range missing {
		if _, ok := out[id]; ok {
			continue
		}
		if data, err := s.pullFromPeers(ctx, id); err == nil {
			out[id] = data
		}
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
		// 空の data は本体がアプリサーバのローカルにある行で、JPEG ではない。
		if len(data) == 0 {
			continue
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

func putImageBlobHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		id := c.Param("imageId")
		data, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}
		if !validImageFileID(id) || len(data) == 0 {
			return c.NoContent(http.StatusBadRequest)
		}
		if err := h.PostImages.saveIfAbsent(id, data); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// getImageBlobHandler はローカルのファイルだけを返す。DB やピアを見に行くとピア同士で呼び合うため。
func getImageBlobHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		data, err := h.PostImages.Read(c.Param("imageId"))
		if err != nil {
			return c.NoContent(http.StatusNotFound)
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
