package isureal

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/jmoiron/sqlx"
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
		dir = filepath.Join(os.TempDir(), "isureal-post-images")
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

// ResetFromDB はディレクトリを空にして、未削除投稿が参照する画像だけを書き直す。
func (s *PostImageStore) ResetFromDB(ctx context.Context, db *sqlx.DB) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.dir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	rows, err := db.QueryxContext(ctx, `
   SELECT i.id, i.data
   FROM image_objects i
   WHERE i.id IN (SELECT image_id FROM posts WHERE deleted_at IS NULL)
  `, preparedQuery)
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
		if err := s.save(id, data); err != nil {
			return err
		}
	}
	return rows.Err()
}

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
	query, args, err := sqlx.In(`SELECT id, data FROM image_objects WHERE id IN (?)`, missing)
	if err != nil {
		return nil, err
	}
	query = sqlx.Rebind(sqlx.DOLLAR, query)
	rows, err := db.QueryxContext(ctx, query, append([]any{preparedQuery}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if err := s.Save(id, data); err != nil {
			return nil, err
		}
		out[id] = data
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func contestAppIPs() []net.IP {
	return []net.IP{
		net.ParseIP("192.168.1.10"),
		net.ParseIP("192.168.2.10"),
		net.ParseIP("192.168.3.10"),
	}
}

func localIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP == nil {
			continue
		}
		ips = append(ips, ipnet.IP)
	}
	return ips, nil
}

func ipInList(list []net.IP, raw string) bool {
	ip := net.ParseIP(raw)
	if ip == nil {
		return false
	}
	for _, item := range list {
		if item.Equal(ip) {
			return true
		}
	}
	return false
}

func runningOnContestApp() bool {
	locals, err := localIPs()
	if err != nil {
		return false
	}
	for _, contest := range contestAppIPs() {
		for _, local := range locals {
			if local.Equal(contest) {
				return true
			}
		}
	}
	return false
}
