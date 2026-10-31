package isureal

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

type persistJob struct {
	post        PostWithAuthor
	imageOrigin string
	generation  uint64
}

type persistState struct {
	job  persistJob
	done chan struct{}
	err  error
	skip bool
	mu   sync.Mutex
}

// postPersistQueue はメモリ上で受けた投稿を Primary へ遅延書き込みする。
type postPersistQueue struct {
	h          *HandlerContext
	mu         sync.Mutex
	pending    map[string]*persistState
	wg         sync.WaitGroup
	generation atomic.Uint64
}

func startPostPersistQueue(h *HandlerContext) *postPersistQueue {
	return &postPersistQueue{
		h:       h,
		pending: map[string]*persistState{},
	}
}

func (q *postPersistQueue) Enqueue(post PostWithAuthor, imageOrigin string) {
	st := &persistState{
		job: persistJob{
			post:        post,
			imageOrigin: imageOrigin,
			generation:  q.generation.Load(),
		},
		done: make(chan struct{}),
	}
	q.mu.Lock()
	q.pending[post.ID] = st
	q.mu.Unlock()
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		q.run(st)
	}()
}

func (q *postPersistQueue) MarkDeleted(postID string) {
	q.mu.Lock()
	st := q.pending[postID]
	q.mu.Unlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	st.skip = true
	st.mu.Unlock()
}

// Ensure は postID の遅延書き込み完了を待つ。キューに無ければ何もしない。
func (q *postPersistQueue) Ensure(ctx context.Context, postID string) error {
	q.mu.Lock()
	st := q.pending[postID]
	q.mu.Unlock()
	if st == nil {
		return nil
	}
	select {
	case <-st.done:
		return st.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CancelPending は未書き込みを破棄して完了を待つ。initialize の DB 初期化前に呼ぶ。
func (q *postPersistQueue) CancelPending() {
	q.generation.Add(1)
	q.mu.Lock()
	for _, st := range q.pending {
		st.mu.Lock()
		st.skip = true
		st.mu.Unlock()
	}
	q.mu.Unlock()
	q.wg.Wait()
	q.mu.Lock()
	q.pending = map[string]*persistState{}
	q.mu.Unlock()
}

func (q *postPersistQueue) run(st *persistState) {
	defer close(st.done)
	st.mu.Lock()
	skip := st.skip
	job := st.job
	st.mu.Unlock()
	if skip || job.generation != q.generation.Load() {
		q.forget(job.post.ID)
		return
	}
	st.err = q.persist(job)
	if st.err != nil {
		log.Printf("post persist %s: %v", job.post.ID, st.err)
	}
	q.forget(job.post.ID)
}

func (q *postPersistQueue) forget(postID string) {
	q.mu.Lock()
	delete(q.pending, postID)
	q.mu.Unlock()
}

func (q *postPersistQueue) persist(job persistJob) error {
	if job.generation != q.generation.Load() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := beginLocalTx(ctx, q.h.DB)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if job.generation != q.generation.Load() {
		return nil
	}
	post := job.post.Post
	if _, err := tx.ExecContext(ctx, `
  INSERT INTO image_objects (id, owner_user_id, data, media_type, origin_region)
  VALUES ($1, $2, ''::bytea, 'image/jpeg', $3)
 `, preparedQuery, post.ImageID, post.AuthorID, job.imageOrigin); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
  INSERT INTO posts (
    id, author_id, posting_window_id, image_id, caption, visibility,
    location_kind, latitude, longitude, locality, posted_at
  ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
 `, post.ID, post.AuthorID, post.PostingWindowID, post.ImageID, post.Caption, post.Visibility,
		post.Kind, post.Latitude, post.Longitude, post.Locality, post.PostedAt); err != nil {
		return err
	}
	if job.generation != q.generation.Load() {
		return nil
	}
	return tx.Commit()
}
