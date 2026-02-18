package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/attachments"
)

type ImportAttachmentTool struct {
	workspace string
	restrict  bool
	store     *attachments.Store
}

func NewImportAttachmentTool(workspace string, restrict bool, store *attachments.Store) *ImportAttachmentTool {
	return &ImportAttachmentTool{
		workspace: workspace,
		restrict:  restrict,
		store:     store,
	}
}

func (t *ImportAttachmentTool) Name() string {
	return "import_attachment"
}

func (t *ImportAttachmentTool) Description() string {
	return "Import a saved attachment into the workspace so other file tools can operate on it"
}

func (t *ImportAttachmentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"attachment_id": map[string]interface{}{
				"type":        "string",
				"description": "Attachment ID from attachment_saved marker (preferred)",
			},
			"source_path": map[string]interface{}{
				"type":        "string",
				"description": "Direct source path in quarantine (fallback if no attachment_id)",
			},
			"target_path": map[string]interface{}{
				"type":        "string",
				"description": "Destination path in workspace",
			},
			"overwrite": map[string]interface{}{
				"type":        "boolean",
				"description": "Overwrite destination if it already exists",
			},
		},
		"required": []string{"target_path"},
	}
}

func (t *ImportAttachmentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	targetPath, ok := args["target_path"].(string)
	if !ok || targetPath == "" {
		return ErrorResult("target_path is required")
	}
	overwrite, _ := args["overwrite"].(bool)

	var srcPath string
	var attachmentID string
	if v, ok := args["attachment_id"].(string); ok && v != "" {
		attachmentID = v
		rec, found := t.store.GetByID(v)
		if !found {
			return ErrorResult(fmt.Sprintf("attachment not found: %s", v))
		}
		srcPath = rec.StoredPath
	} else if v, ok := args["source_path"].(string); ok && v != "" {
		srcPath = v
	} else {
		return ErrorResult("attachment_id or source_path is required")
	}

	if !t.store.IsInRoot(srcPath) {
		return ErrorResult("source_path is outside attachment quarantine root")
	}

	resolvedTarget, err := validatePath(targetPath, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	if _, err := os.Stat(srcPath); err != nil {
		return ErrorResult(fmt.Sprintf("failed to read source file: %v", err))
	}

	if _, err := os.Stat(resolvedTarget); err == nil && !overwrite {
		return ErrorResult("target already exists; set overwrite=true to replace it")
	}

	if err := os.MkdirAll(filepath.Dir(resolvedTarget), 0755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create target directory: %v", err))
	}

	bytesCopied, err := copyFile(srcPath, resolvedTarget)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to import attachment: %v", err))
	}

	if attachmentID != "" {
		_ = t.store.MarkImported(attachmentID, resolvedTarget)
	}

	return NewToolResult(fmt.Sprintf("Attachment imported: %s (%d bytes)", resolvedTarget, bytesCopied))
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	return io.Copy(out, in)
}
