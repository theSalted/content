package objects

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilesystemPrivateAtomicAndContained(t *testing.T) {
	root := t.TempDir()
	f, e := NewFilesystem(root)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	key := "v1/" + strings.Repeat("a", 64) + "/" + strings.Repeat("b", 64) + ".png"
	data := []byte("synthetic object bytes")
	for i := 0; i < 2; i++ {
		if e = f.Put(context.Background(), key, data, "image/png"); e != nil {
			t.Fatal(e)
		}
	}
	r, e := f.Get(context.Background(), key)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(data, got) {
		t.Fatal("bytes changed")
	}
	info, _ := os.Stat(filepath.Join(root, key))
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe object permissions")
	}
	for _, bad := range []string{"../outside", "/etc/passwd", "v1/../../secret"} {
		if e = f.Put(context.Background(), bad, data, "image/png"); e == nil {
			t.Fatal("traversal accepted")
		}
	}
	outside := t.TempDir()
	linked := "v1/" + strings.Repeat("c", 64)
	if e = os.Symlink(outside, filepath.Join(root, linked)); e != nil {
		t.Fatal(e)
	}
	if e = f.Put(context.Background(), linked+"/"+strings.Repeat("d", 64)+".png", data, "image/png"); e == nil {
		t.Fatal("symlink escaped")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("outside root changed")
	}
	temp := filepath.Join(root, "v1", strings.Repeat("a", 64), ".upload-"+strings.Repeat("A", 26))
	if e = os.WriteFile(temp, data, 0600); e != nil {
		t.Fatal(e)
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(temp, old, old)
	n, e := f.CleanupTemporary(context.Background(), time.Now().Add(-24*time.Hour))
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if _, e = os.Stat(filepath.Join(root, key)); e != nil {
		t.Fatal("ready object removed")
	}
	if e = f.Delete(context.Background(), key); e != nil {
		t.Fatal(e)
	}
	if e = f.Delete(context.Background(), key); e != nil {
		t.Fatal("delete not idempotent", e)
	}
}
