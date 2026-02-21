package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DebugLogsTool reads recent log entries so the LLM can self-diagnose issues.
type DebugLogsTool struct {
	workspace string
}

func NewDebugLogsTool(workspace string) *DebugLogsTool {
	return &DebugLogsTool{workspace: workspace}
}

func (t *DebugLogsTool) Name() string { return "debug_logs" }

func (t *DebugLogsTool) Description() string {
	return "Read recent PicoClaw log entries to diagnose issues, errors, or unexpected behavior. Use this when the user asks why something failed or wants to debug a problem. Supports filtering by log level, keyword, or correlation_id."
}

func (t *DebugLogsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"lines": map[string]interface{}{
				"type":        "integer",
				"description": "Number of recent log lines to read (default: 50, max: 200)",
			},
			"correlation_id": map[string]interface{}{
				"type":        "string",
				"description": "Filter logs to a specific request by correlation_id",
			},
			"keyword": map[string]interface{}{
				"type":        "string",
				"description": "Filter logs containing this keyword in message or fields",
			},
			"level": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"ERROR", "WARN", "INFO", "DEBUG"},
				"description": "Filter by minimum log level (e.g. ERROR shows only errors, WARN shows warnings and errors)",
			},
		},
	}
}

// logEntry represents a parsed JSON log line.
type logEntry struct {
	Level     string                 `json:"level"`
	Timestamp string                 `json:"timestamp"`
	Component string                 `json:"component"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields"`
	Caller    string                 `json:"caller"`
}

// levelPriority returns a numeric priority for log level filtering.
func levelPriority(level string) int {
	switch strings.ToUpper(level) {
	case "ERROR":
		return 4
	case "WARN":
		return 3
	case "INFO":
		return 2
	case "DEBUG":
		return 1
	default:
		return 0
	}
}

func (t *DebugLogsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	logPath := filepath.Join(t.workspace, "picoclaw.log")

	maxLines := 50
	if l, ok := args["lines"].(float64); ok && l > 0 {
		maxLines = int(l)
		if maxLines > 200 {
			maxLines = 200
		}
	}

	correlationID := ""
	if cid, ok := args["correlation_id"].(string); ok {
		correlationID = cid
	}

	keyword := ""
	if kw, ok := args["keyword"].(string); ok {
		keyword = strings.ToLower(kw)
	}

	minLevel := 0
	if lvl, ok := args["level"].(string); ok && lvl != "" {
		minLevel = levelPriority(lvl)
	}

	// Read the log file tail
	lines, err := readTail(logPath, maxLines*3) // read extra to have room after filtering
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to read log file: %v", err))
	}

	if len(lines) == 0 {
		return SilentResult("Log file is empty.")
	}

	// Parse and filter
	var filtered []logEntry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry logEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip non-JSON lines
		}

		// Filter by level
		if minLevel > 0 && levelPriority(entry.Level) < minLevel {
			continue
		}

		// Filter by correlation_id
		if correlationID != "" {
			cid, _ := entry.Fields["correlation_id"].(string)
			if cid != correlationID {
				continue
			}
		}

		// Filter by keyword
		if keyword != "" {
			found := strings.Contains(strings.ToLower(entry.Message), keyword)
			if !found {
				// Also search in fields
				fieldsJSON, _ := json.Marshal(entry.Fields)
				found = strings.Contains(strings.ToLower(string(fieldsJSON)), keyword)
			}
			if !found {
				continue
			}
		}

		filtered = append(filtered, entry)
	}

	// Keep only last maxLines entries after filtering
	if len(filtered) > maxLines {
		filtered = filtered[len(filtered)-maxLines:]
	}

	if len(filtered) == 0 {
		return SilentResult("No log entries matched the filters.")
	}

	// Format for LLM readability
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== PicoClaw Logs (%d entries) ===\n\n", len(filtered)))

	for _, entry := range filtered {
		sb.WriteString(fmt.Sprintf("[%s] %s [%s] %s", entry.Timestamp, entry.Level, entry.Component, entry.Message))

		// Include key fields (skip verbose ones)
		if len(entry.Fields) > 0 {
			interesting := make(map[string]interface{})
			for k, v := range entry.Fields {
				// Skip very long values but keep the important ones
				if s, ok := v.(string); ok && len(s) > 200 {
					interesting[k] = s[:200] + "..."
				} else {
					interesting[k] = v
				}
			}
			fieldsJSON, _ := json.Marshal(interesting)
			sb.WriteString(fmt.Sprintf(" %s", string(fieldsJSON)))
		}
		sb.WriteString("\n")
	}

	result := sb.String()
	// Truncate to avoid flooding LLM context
	if len(result) > 8000 {
		result = result[len(result)-8000:]
		result = "... (truncated)\n" + result
	}

	return SilentResult(result)
}

// readTail reads the last n lines from a file.
func readTail(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var allLines []string
	scanner := bufio.NewScanner(f)
	// Increase buffer size for long JSON log lines
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(allLines) > n {
		return allLines[len(allLines)-n:], nil
	}
	return allLines, nil
}
