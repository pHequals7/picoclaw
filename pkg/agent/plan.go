package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	minPlanBullets = 4
	maxPlanBullets = 6
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
	bullets := make([]string, 0, maxPlanBullets)

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
		if len(bullets) >= maxPlanBullets-1 {
			break
		}
	}

	// Ensure stable 4-6 bullets for trust/readability.
	defaults := []string{
		"Validate intermediate results",
		"Retry failed steps if needed",
		"Summarize outcome and next actions",
	}
	for _, d := range defaults {
		if len(bullets) >= minPlanBullets {
			break
		}
		bullets = append(bullets, d)
	}
	if len(bullets) == 0 {
		bullets = []string{
			"Inspect the request context",
			"Execute required tools in sequence",
			"Validate intermediate results",
			"Summarize outcome and next actions",
		}
	}
	return bullets
}

func formatExecutionPlanProgress(bullets []string) string {
	if len(bullets) == 0 {
		return "Plan:\n1. Analyze request\n2. Execute required actions\n3. Validate results\n4. Respond with outcome"
	}

	lines := []string{"Execution plan:"}
	for i, b := range bullets {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, b))
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
