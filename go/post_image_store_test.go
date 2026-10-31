package isureal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPostImageStoreSaveAndRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ISUREAL_POST_IMAGE_DIR", dir)
	store, err := NewPostImageStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("abc-123", []byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read("abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "jpeg" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "abc-123")); err != nil {
		t.Fatal(err)
	}
}

func TestPostImageStoreRejectsPath(t *testing.T) {
	store, err := NewPostImageStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("../etc/passwd", []byte("x")); err == nil {
		t.Fatal("expected error")
	}
}
