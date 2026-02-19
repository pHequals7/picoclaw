package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestBuildExecutionPlanBullets_NoDefaultPadding(t *testing.T) {
	calls := []providers.ToolCall{
		{Name: "read_file", Arguments: map[string]interface{}{"path": "/tmp/a.txt"}},
		{Name: "exec", Arguments: map[string]interface{}{"command": "rg foo ."}},
	}

	bullets := buildExecutionPlanBullets(calls)
	if len(bullets) != 2 {
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

func TestFormatExecutionPlanProgressWithArtifact(t *testing.T) {
	msg := formatExecutionPlanProgressWithArtifact([]string{
		"Read config file",
		"Run validation commands",
		"Write patch",
		"Summarize results",
	}, "/home/ubuntu/.picoclaw/workspace/plans/2026-02-18_150000_read-config-file.md")

	if !strings.Contains(msg, "Plan file:") {
		t.Fatalf("missing plan file line: %q", msg)
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

func TestWriteExecutionPlanFile(t *testing.T) {
	tmp := t.TempDir()
	now := time.Date(2026, 2, 18, 15, 4, 5, 0, time.UTC)

	path, err := writeExecutionPlanFile(tmp, []string{
		"Read config file",
		"Run validation commands",
		"Write patch",
		"Summarize results",
	}, planFileMetadata{
		SessionKey:    "telegram:8138716728",
		CorrelationID: "8138716728-8138716728-1771426293940",
		Model:         "claude-sonnet-4-6",
	}, now)
	if err != nil {
		t.Fatalf("writeExecutionPlanFile() error: %v", err)
	}

	if !strings.HasPrefix(path, filepath.Join(tmp, "plans")+string(os.PathSeparator)) {
		t.Fatalf("unexpected plan path: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan file: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, `session_key: "telegram:8138716728"`) {
		t.Fatalf("missing session metadata: %s", text)
	}
	if !strings.Contains(text, "# Execution Plan") {
		t.Fatalf("missing title: %s", text)
	}
	if !strings.Contains(text, "1. Read config file") {
		t.Fatalf("missing bullets: %s", text)
	}
}

func TestParseExecutionPlanBullets_Numbered(t *testing.T) {
	raw := "1. Read requirements\n2. Inspect target files\n3. Apply patch\n4. Run tests\n"
	got := parseExecutionPlanBullets(raw)
	if len(got) != 4 {
		t.Fatalf("expected 4 bullets, got %d (%v)", len(got), got)
	}
	if got[0] != "Read requirements" {
		t.Fatalf("unexpected first bullet: %q", got[0])
	}
}
