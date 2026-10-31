package isureal

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func newImagePeer(t *testing.T) (*PostImageStore, string) {
	t.Setenv("ISUREAL_POST_IMAGE_DIR", t.TempDir())
	t.Setenv("ISUREAL_IMAGE_CACHE_DIR", t.TempDir())
	store, err := NewPostImageStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &HandlerContext{PostImages: store}
	e := echo.New()
	e.PUT("/internal/image-blob/:imageId", putImageBlobHandler(h))
	e.GET("/internal/image-blob/:imageId", getImageBlobHandler(h))
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return store, srv.URL
}

func TestReplicate(t *testing.T) {
	peer, peerURL := newImagePeer(t)
	jpeg := []byte("jpeg-body")
	ctx := context.Background()

	t.Setenv("ISUREAL_POST_IMAGE_DIR", t.TempDir())
	t.Setenv("ISUREAL_IMAGE_CACHE_DIR", t.TempDir())
	writer, err := NewPostImageStore([]string{peerURL})
	if err != nil {
		t.Fatal(err)
	}
	if got := writer.Replicate(ctx, "img-1", jpeg)(); len(got) != 0 {
		t.Fatalf("DB body = %q, want empty when every peer stored it", got)
	}
	if got, err := peer.Read("img-1"); err != nil || !bytes.Equal(got, jpeg) {
		t.Fatalf("peer file = %q, %v", got, err)
	}
	if got, err := writer.Read("img-1"); err != nil || !bytes.Equal(got, jpeg) {
		t.Fatalf("local file = %q, %v", got, err)
	}

	if err := writer.Clear(); err != nil {
		t.Fatal(err)
	}
	if got, err := writer.pullFromPeers(ctx, "img-1"); err != nil || !bytes.Equal(got, jpeg) {
		t.Fatalf("pulled = %q, %v", got, err)
	}
	if _, err := writer.pullFromPeers(ctx, "img-unknown"); err != errImageBodyMissing {
		t.Fatalf("pull unknown err = %v", err)
	}

	broken, err := NewPostImageStore([]string{peerURL, "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := broken.Replicate(ctx, "img-2", jpeg)(); !bytes.Equal(got, jpeg) {
		t.Fatalf("DB body = %q, want full JPEG when a peer is down", got)
	}
}
