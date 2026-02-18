package agent

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestBuildExecutionPlanBullets_CountBounds(t *testing.T) {
	calls := []providers.ToolCall{
		{Name: "read_file", Arguments: map[string]interface{}{"path": "/tmp/a.txt"}},
		{Name: "exec", Arguments: map[string]interface{}{"command": "rg foo ."}},
	}

	bullets := buildExecutionPlanBullets(calls)
	if len(bullets) < minPlanBullets || len(bullets) > maxPlanBullets {
		t.Fatalf("unexpected bullet count: %d", len(bullets))
	}
}

func TestBuildExecutionPlanBullets_Deduplicates(t *testing.T) {
	calls := []providers.ToolCall{
		{Name: "read_file", Arguments: map[string]interface{}{"path": "/tmp/a.txt"}},
		{Name: "read_file", Arguments: map[string]interface{}{"path": "/tmp/a.txt"}},
	}

	bullets := buildExecutionPlanBullets(calls)
	seen := map[string]struct{}{}
	for _, b := range bullets {
		if _, ok := seen[b]; ok {
			t.Fatalf("duplicate bullet found: %s", b)
		}
		seen[b] = struct{}{}
	}
}

func TestFormatExecutionPlanProgress(t *testing.T) {
	msg := formatExecutionPlanProgress([]string{
		"Read config file",
		"Run validation commands",
		"Write patch",
		"Summarize results",
	})

	if !strings.Contains(msg, "Execution plan:") {
		t.Fatalf("missing execution plan heading: %q", msg)
	}
	if !strings.Contains(msg, "1. Read config file") {
		t.Fatalf("missing first bullet numbering: %q", msg)
	}
	if !strings.Contains(msg, "Note: plan may adapt if a step fails.") {
		t.Fatalf("missing adaptation note: %q", msg)
	}
}

func TestSummarizeToolCallForPlan_Exec(t *testing.T) {
	tc := providers.ToolCall{
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "go test ./pkg/agent"},
	}
	got := summarizeToolCallForPlan(tc)
	if !strings.Contains(got, "Run go test") {
		t.Fatalf("unexpected exec summary: %q", got)
	}
}

func TestExecutionPlanState_AbsorbAndAllow(t *testing.T) {
	state := newExecutionPlanState()
	state.absorbToolCalls([]providers.ToolCall{
		{Name: "read_file"},
		{Name: "exec"},
	})

	if !state.isAllowedTool("read_file") || !state.isAllowedTool("exec") {
		t.Fatalf("expected tools to be allowed after absorb")
	}
	if state.isAllowedTool("write_file") {
		t.Fatalf("unexpected allowed tool")
	}
}
