package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SendFileCallback is called to send files via the message bus.
type SendFileCallback func(channel, chatID, caption string, files []string) error

// SendFileTool allows the agent to send local files to the user via their chat channel.
type SendFileTool struct {
	sendCallback   SendFileCallback
	defaultChannel string
	defaultChatID  string
	workspace      string
}

func NewSendFileTool(workspace string) *SendFileTool {
	return &SendFileTool{workspace: workspace}
}

func (t *SendFileTool) Name() string {
	return "send_file"
}

func (t *SendFileTool) Description() string {
	return "Send one or more files (images, videos, documents, etc.) to the user via their chat channel. Files must exist on the local filesystem."
}

func (t *SendFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"files": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Array of local file paths to send",
			},
			"caption": map[string]interface{}{
				"type":        "string",
				"description": "Optional caption/message to accompany the files",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target channel (telegram, etc.)",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target chat/user ID",
			},
		},
		"required": []string{"files"},
	}
}

func (t *SendFileTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
}

func (t *SendFileTool) SetSendCallback(callback SendFileCallback) {
	t.sendCallback = callback
}

func (t *SendFileTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	// Parse files array
	filesRaw, ok := args["files"]
	if !ok {
		return &ToolResult{ForLLM: "files parameter is required", IsError: true}
	}

	filesSlice, ok := filesRaw.([]interface{})
	if !ok {
		return &ToolResult{ForLLM: "files must be an array of file paths", IsError: true}
	}

	if len(filesSlice) == 0 {
		return &ToolResult{ForLLM: "files array is empty", IsError: true}
	}

	caption, _ := args["caption"].(string)
	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)

	if channel == "" {
		channel = t.defaultChannel
	}
	if chatID == "" {
		chatID = t.defaultChatID
	}

	if channel == "" || chatID == "" {
		return &ToolResult{ForLLM: "No target channel/chat specified", IsError: true}
	}

	if t.sendCallback == nil {
		return &ToolResult{ForLLM: "File sending not configured", IsError: true}
	}

	// Validate and resolve file paths
	var validFiles []string
	for _, f := range filesSlice {
		filePath, ok := f.(string)
		if !ok {
			continue
		}

		// Block path traversal
		if strings.Contains(filePath, "..") {
			return &ToolResult{
				ForLLM:  fmt.Sprintf("path traversal not allowed: %s", filePath),
				IsError: true,
			}
		}

		// Resolve relative paths against workspace
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(t.workspace, filePath)
		}

		// Verify file exists
		info, err := os.Stat(filePath)
		if err != nil {
			return &ToolResult{
				ForLLM:  fmt.Sprintf("file not found: %s", filePath),
				IsError: true,
			}
		}
		if info.IsDir() {
			return &ToolResult{
				ForLLM:  fmt.Sprintf("path is a directory, not a file: %s", filePath),
				IsError: true,
			}
		}

		validFiles = append(validFiles, filePath)
	}

	if len(validFiles) == 0 {
		return &ToolResult{ForLLM: "no valid files to send", IsError: true}
	}

	if err := t.sendCallback(channel, chatID, caption, validFiles); err != nil {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("sending files: %v", err),
			IsError: true,
			Err:     err,
		}
	}

	return &ToolResult{
		ForLLM: fmt.Sprintf("Sent %d file(s) to %s:%s", len(validFiles), channel, chatID),
		Silent: true,
	}
}
