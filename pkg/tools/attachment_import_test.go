package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/attachments"
)

func TestImportAttachmentToolByID(t *testing.T) {
	workspace := t.TempDir()
	src := filepath.Join(workspace, "src.txt")
	if err := os.WriteFile(src, []byte("abc"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	store := attachments.NewStore(workspace)
	rec, err := store.SaveFromLocalFile("telegram", "1", "u1", "m1", "src.txt", "text/plain", "document", src)
	if err != nil {
		t.Fatalf("save attachment: %v", err)
	}

	tool := NewImportAttachmentTool(workspace, true, store)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"attachment_id": rec.ID,
		"target_path":   "imports/src.txt",
	})
	if res.IsError {
		t.Fatalf("expected success: %s", res.ForLLM)
	}
	out := filepath.Join(workspace, "imports", "src.txt")
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("unexpected output content: %q", string(got))
	}
}

func TestImportAttachmentToolRejectsOutsideRoot(t *testing.T) {
	workspace := t.TempDir()
	tool := NewImportAttachmentTool(workspace, true, attachments.NewStore(workspace))
	res := tool.Execute(context.Background(), map[string]interface{}{
		"source_path": "/tmp/not-in-attachments.txt",
		"target_path": "imports/x.txt",
	})
	if !res.IsError {
		t.Fatalf("expected error")
	}
	if !strings.Contains(res.ForLLM, "outside attachment quarantine root") {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
}
