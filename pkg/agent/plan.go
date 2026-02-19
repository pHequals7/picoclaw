package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type executionPlanState struct {
	Announced bool
	Bullets   []string
	Allowed   map[string]struct{}
}

func newExecutionPlanState() *executionPlanState {
	return &executionPlanState{
		Allowed: make(map[string]struct{}),
	}
}

func (s *executionPlanState) absorbToolCalls(calls []providers.ToolCall) {
	for _, tc := range calls {
		name := strings.TrimSpace(tc.Name)
		if name == "" && tc.Function != nil {
			name = strings.TrimSpace(tc.Function.Name)
		}
		if name == "" {
			continue
		}
		s.Allowed[name] = struct{}{}
	}
}

func (s *executionPlanState) isAllowedTool(name string) bool {
	_, ok := s.Allowed[name]
	return ok
}

func buildExecutionPlanBullets(toolCalls []providers.ToolCall) []string {
	seen := make(map[string]struct{})
	bullets := make([]string, 0, len(toolCalls))

	for _, tc := range toolCalls {
		step := summarizeToolCallForPlan(tc)
		if step == "" {
			continue
		}
		if _, ok := seen[step]; ok {
			continue
		}
		seen[step] = struct{}{}
		bullets = append(bullets, step)
	}
	return bullets
}

func formatExecutionPlanProgress(bullets []string) string {
	return formatExecutionPlanProgressWithArtifact(bullets, "")
}

func formatExecutionPlanProgressWithArtifact(bullets []string, planPath string) string {
	if len(bullets) == 0 {
		return "Execution plan:\n- (planner returned no steps)"
	}

	lines := []string{"Execution plan:"}
	for i, b := range bullets {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, b))
	}
	if planPath != "" {
		lines = append(lines, fmt.Sprintf("Plan file: `%s`", planPath))
	}
	lines = append(lines, "Note: plan may adapt if a step fails.")
	return strings.Join(lines, "\n")
}

func formatPlanContextMessage(bullets []string) string {
	lines := []string{"Execution plan locked for this turn:"}
	for i, b := range bullets {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, b))
	}
	return strings.Join(lines, "\n")
}

func formatPlanUpdateProgress(step string) string {
	return fmt.Sprintf("Plan update:\n- %s", step)
}

func parseExecutionPlanBullets(raw string) []string {
	lines := strings.Split(raw, "\n")
	bullets := make([]string, 0, len(lines))
	seen := make(map[string]struct{})

	for _, line := range lines {
		v := strings.TrimSpace(line)
		if v == "" {
			continue
		}
		lower := strings.ToLower(v)
		if strings.Contains(lower, "execution plan") || strings.Contains(lower, "plan file:") || strings.Contains(lower, "note:") {
			continue
		}
		if numberedPlanPrefix.MatchString(v) {
			v = numberedPlanPrefix.ReplaceAllString(v, "")
		} else if bulletPlanPrefix.MatchString(v) {
			v = bulletPlanPrefix.ReplaceAllString(v, "")
		} else {
			// Skip plain prose lines. We only accept explicit bullets/numbered steps.
			continue
		}

		v = strings.Trim(strings.TrimSpace(v), "`*_")
		if v == "" {
			continue
		}
		if len(v) > 120 {
			v = strings.TrimSpace(v[:120])
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		bullets = append(bullets, v)
	}

	return bullets
}

type planFileMetadata struct {
	SessionKey    string
	CorrelationID string
	Model         string
}

func writeExecutionPlanFile(workspace string, bullets []string, meta planFileMetadata, now time.Time) (string, error) {
	planDir := filepath.Join(workspace, "plans")
	if err := os.MkdirAll(planDir, 0755); err != nil {
		return "", err
	}

	base := strings.TrimSpace(firstNonEmptyPlanStep(bullets))
	if base == "" {
		base = "task"
	}
	slug := slugifyPlanTitle(base)
	filename := fmt.Sprintf("%s_%s.md", now.UTC().Format("2006-01-02_150405"), slug)
	path := filepath.Join(planDir, filename)

	var lines []string
	lines = append(lines, "---")
	lines = append(lines, fmt.Sprintf("session_key: %q", meta.SessionKey))
	lines = append(lines, fmt.Sprintf("correlation_id: %q", meta.CorrelationID))
	lines = append(lines, fmt.Sprintf("model: %q", meta.Model))
	lines = append(lines, fmt.Sprintf("created_at_utc: %q", now.UTC().Format(time.RFC3339)))
	lines = append(lines, "plan_mode: true")
	lines = append(lines, "---")
	lines = append(lines, "")
	lines = append(lines, "# Execution Plan")
	lines = append(lines, "")
	for i, b := range bullets {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, b))
	}
	lines = append(lines, "")
	lines = append(lines, "_Note: plan may adapt if a step fails._")
	content := strings.Join(lines, "\n") + "\n"

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return path, nil
}

func firstNonEmptyPlanStep(bullets []string) string {
	for _, b := range bullets {
		if strings.TrimSpace(b) != "" {
			return b
		}
	}
	return ""
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)
var numberedPlanPrefix = regexp.MustCompile(`^\d+\s*[\).\-\:]\s+`)
var bulletPlanPrefix = regexp.MustCompile(`^[-*•]\s+`)

func slugifyPlanTitle(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = nonSlugChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "task"
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		return "task"
	}
	return s
}

func summarizeToolCallForPlan(tc providers.ToolCall) string {
	name := strings.TrimSpace(tc.Name)
	if name == "" && tc.Function != nil {
		name = strings.TrimSpace(tc.Function.Name)
	}
	if name == "" {
		return "Execute required operation"
	}

	switch name {
	case "exec":
		cmd, _ := tc.Arguments["command"].(string)
		return summarizeCommandForPlan(cmd)
	case "read_file":
		if p, _ := tc.Arguments["path"].(string); p != "" {
			return fmt.Sprintf("Read %s", shortenPlanPath(p))
		}
		return "Read required files"
	case "write_file":
		if p, _ := tc.Arguments["path"].(string); p != "" {
			return fmt.Sprintf("Write %s", shortenPlanPath(p))
		}
		return "Write updated files"
	case "list_dir":
		if p, _ := tc.Arguments["path"].(string); p != "" {
			return fmt.Sprintf("List %s", shortenPlanPath(p))
		}
		return "List directory contents"
	case "web_search":
		return "Search the web for required context"
	case "web_fetch":
		return "Fetch and inspect referenced content"
	case "spawn", "subagent":
		return "Delegate a focused subtask"
	default:
		return fmt.Sprintf("Run %s", name)
	}
}

func summarizeCommandForPlan(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "Run shell command"
	}

	segments := []string{" && ", " | ", " ; "}
	for _, seg := range segments {
		if idx := strings.Index(cmd, seg); idx > 0 {
			cmd = cmd[:idx]
			break
		}
	}

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "Run shell command"
	}

	base := parts[0]
	switch base {
	case "ls", "dir":
		return "Inspect directory contents"
	case "cat", "head", "tail":
		return "Inspect file contents"
	case "rg", "grep", "find":
		return "Search codebase for relevant entries"
	case "go":
		if len(parts) > 1 {
			return fmt.Sprintf("Run go %s", parts[1])
		}
		return "Run go command"
	case "git":
		if len(parts) > 1 {
			return fmt.Sprintf("Run git %s", parts[1])
		}
		return "Run git command"
	case "npm", "pnpm", "yarn":
		if len(parts) > 1 {
			return fmt.Sprintf("Run %s %s", base, parts[1])
		}
		return fmt.Sprintf("Run %s command", base)
	case "python", "python3":
		return "Run Python script"
	default:
		return fmt.Sprintf("Run command: %s", utils.Truncate(strings.Join(parts[:minInt(3, len(parts))], " "), 48))
	}
}

func shortenPlanPath(p string) string {
	clean := strings.TrimSpace(p)
	if clean == "" {
		return "target file"
	}
	base := filepath.Base(clean)
	if base == "." || base == "/" {
		return clean
	}
	return base
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
