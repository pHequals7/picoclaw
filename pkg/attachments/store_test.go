package attachments

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveAndGetByID(t *testing.T) {
	tmp := t.TempDir()
	in := filepath.Join(tmp, "in.txt")
	if err := os.WriteFile(in, []byte("hello"), 0644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	s := NewStore(tmp)
	rec, err := s.SaveFromLocalFile("telegram", "123", "u1", "m1", "demo.txt", "text/plain", "document", in)
	if err != nil {
		t.Fatalf("SaveFromLocalFile failed: %v", err)
	}
	if rec.ID == "" || rec.StoredPath == "" {
		t.Fatalf("unexpected empty record: %+v", rec)
	}
	if _, err := os.Stat(rec.StoredPath); err != nil {
		t.Fatalf("stored file missing: %v", err)
	}

	got, ok := s.GetByID(rec.ID)
	if !ok {
		t.Fatalf("record not found by id")
	}
	if got.Name != "demo.txt" {
		t.Fatalf("name mismatch: got %q", got.Name)
	}
}

func TestMarkImported(t *testing.T) {
	tmp := t.TempDir()
	in := filepath.Join(tmp, "in.txt")
	if err := os.WriteFile(in, []byte("hello"), 0644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	s := NewStore(tmp)
	rec, err := s.SaveFromLocalFile("telegram", "123", "u1", "m1", "demo.txt", "text/plain", "document", in)
	if err != nil {
		t.Fatalf("SaveFromLocalFile failed: %v", err)
	}
	if err := s.MarkImported(rec.ID, "/tmp/workspace/imported.txt"); err != nil {
		t.Fatalf("MarkImported failed: %v", err)
	}
	got, ok := s.GetByID(rec.ID)
	if !ok {
		t.Fatalf("record not found")
	}
	if got.ImportedPath != "/tmp/workspace/imported.txt" {
		t.Fatalf("unexpected imported path: %q", got.ImportedPath)
	}
}
