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

// 投稿画像の JPEG をアプリサーバーのローカルに置く。
// エクスポートは初期データのバイト列をリクエストのたびに DB から運ばない。
type PostImageStore struct {
	dir string
	mu  sync.Mutex
}

func NewPostImageStore() (*PostImageStore, error) {
	dir := os.Getenv("ISUREAL_POST_IMAGE_DIR")
	if dir == "" {
		// 作業ディレクトリは go/ なので webapp/post-images になる。make post-images/sync もここへ配る。
		dir = "../post-images"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &PostImageStore{dir: dir}, nil
}

func (s *PostImageStore) path(id string) (string, error) {
	if !validImageFileID(id) {
		return "", errors.New("invalid image id")
	}
	return filepath.Join(s.dir, id), nil
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
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s *PostImageStore) Save(id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(id, data)
}

func (s *PostImageStore) save(id string, data []byte) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FillMissing は未削除投稿が参照する画像のうち、ファイルが無いものだけを DB から書き出す。
// 画像は ID ごとに中身が変わらないので、置いたファイルは消さない。
func (s *PostImageStore) FillMissing(ctx context.Context, db *sqlx.DB) error {
	var ids []string
	if err := db.SelectContext(ctx, &ids, `SELECT DISTINCT image_id FROM posts WHERE deleted_at IS NULL`); err != nil {
		return err
	}
	var missing []string
	for _, id := range ids {
		path, err := s.path(id)
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

// loadFromDB は ids の画像を DB から読んでファイルに置く。out が nil でなければ中身も入れる。
func (s *PostImageStore) loadFromDB(ctx context.Context, db *sqlx.DB, ids []string, out map[string][]byte) error {
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
		if err := s.Save(id, data); err != nil {
			return err
		}
		if out != nil {
			out[id] = data
		}
	}
	return rows.Err()
}

func fillPostImageCacheHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if err := h.PostImages.FillMissing(c.Request().Context(), h.DB); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
