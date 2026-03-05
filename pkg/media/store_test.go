package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileMediaStore_StoreAndResolve(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.png")
	if err := os.WriteFile(testFile, []byte("fake png data"), 0644); err != nil {
		t.Fatal(err)
	}

	store := NewFileMediaStore()
	ref, err := store.Store(testFile, MediaMeta{
		OriginalName: "test.png",
		MIMEType:     "image/png",
		Scope:        "turn",
	})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if !strings.HasPrefix(ref, "media://") {
		t.Fatalf("ref %q missing media:// prefix", ref)
	}

	// Resolve
	path, err := store.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if path != testFile {
		t.Errorf("Resolve path = %q, want %q", path, testFile)
	}

	// ResolveWithMeta
	path, meta, err := store.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("ResolveWithMeta failed: %v", err)
	}
	if meta.OriginalName != "test.png" {
		t.Errorf("meta.OriginalName = %q, want test.png", meta.OriginalName)
	}
	if meta.SizeBytes != 13 { // len("fake png data")
		t.Errorf("meta.SizeBytes = %d, want 13", meta.SizeBytes)
	}
}

func TestFileMediaStore_ReleaseScope(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "a.png")
	file2 := filepath.Join(dir, "b.png")
	file3 := filepath.Join(dir, "c.png")
	for _, f := range []string{file1, file2, file3} {
		os.WriteFile(f, []byte("data"), 0644)
	}

	store := NewFileMediaStore()
	ref1, _ := store.Store(file1, MediaMeta{Scope: "turn"})
	ref2, _ := store.Store(file2, MediaMeta{Scope: "session"})
	ref3, _ := store.Store(file3, MediaMeta{Scope: "turn"})

	// Release "turn" scope
	store.ReleaseScope("turn")

	// ref1 and ref3 should be gone
	if _, err := store.Resolve(ref1); err == nil {
		t.Error("expected ref1 to be released")
	}
	if _, err := store.Resolve(ref3); err == nil {
		t.Error("expected ref3 to be released")
	}

	// ref2 ("session") should still exist
	if _, err := store.Resolve(ref2); err != nil {
		t.Errorf("ref2 should still exist: %v", err)
	}
}

func TestFileMediaStore_ResolveInvalidRef(t *testing.T) {
	store := NewFileMediaStore()
	_, err := store.Resolve("not-a-media-ref")
	if err == nil {
		t.Error("expected error for invalid ref")
	}

	_, err = store.Resolve("media://nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ref")
	}
}

func TestStreamEncodeFile(t *testing.T) {
	dir := t.TempDir()
	// Write a minimal PNG header (first 8 bytes of PNG signature)
	pngSig := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	testFile := filepath.Join(dir, "test.png")
	if err := os.WriteFile(testFile, pngSig, 0644); err != nil {
		t.Fatal(err)
	}

	mime, b64, err := StreamEncodeFile(testFile, 1024*1024)
	if err != nil {
		t.Fatalf("StreamEncodeFile failed: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	if b64 == "" {
		t.Error("b64 is empty")
	}
}

func TestStreamEncodeFile_LargeFileRejected(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "big.dat")
	// Write 100 bytes
	if err := os.WriteFile(testFile, make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}

	// Set max to 50 bytes
	_, _, err := StreamEncodeFile(testFile, 50)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "exceeds max size") {
		t.Errorf("error = %q, want 'exceeds max size'", err.Error())
	}
}
