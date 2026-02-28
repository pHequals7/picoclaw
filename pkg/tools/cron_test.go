package tools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/cron"
)

type mockJobExecutor struct {
	lastContent    string
	lastSessionKey string
}

func (m *mockJobExecutor) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	m.lastContent = content
	m.lastSessionKey = sessionKey
	return "agent response", nil
}

func newTestCronTool(t *testing.T) (*CronTool, *bus.MessageBus) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "cron.json")
	msgBus := bus.NewMessageBus()
	cronSvc := cron.NewCronService(storePath, nil)
	executor := &mockJobExecutor{}
	tool := NewCronTool(cronSvc, executor, msgBus, t.TempDir())
	return tool, msgBus
}

func TestExecuteJob_Command(t *testing.T) {
	tool, _ := newTestCronTool(t)

	job := &cron.CronJob{
		ID:   "cmd1",
		Name: "test-cmd",
		Payload: cron.CronPayload{
			Kind:    cron.JobTypeCommand,
			Message: "check disk",
			Command: "echo hello",
			Channel: "tg",
			To:      "42",
		},
	}

	result := tool.ExecuteJob(context.Background(), job)
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestExecuteJob_Deliver(t *testing.T) {
	tool, _ := newTestCronTool(t)

	job := &cron.CronJob{
		ID:   "dlv1",
		Name: "test-deliver",
		Payload: cron.CronPayload{
			Kind:    cron.JobTypeDeliver,
			Message: "reminder: meeting at 3pm",
			Channel: "tg",
			To:      "42",
		},
	}

	result := tool.ExecuteJob(context.Background(), job)
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

func TestExecuteJob_Agent(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "cron.json")
	msgBus := bus.NewMessageBus()
	cronSvc := cron.NewCronService(storePath, nil)
	executor := &mockJobExecutor{}
	tool := NewCronTool(cronSvc, executor, msgBus, t.TempDir())

	job := &cron.CronJob{
		ID:   "agt1",
		Name: "test-agent",
		Payload: cron.CronPayload{
			Kind:    cron.JobTypeAgent,
			Message: "generate report",
			Channel: "tg",
			To:      "42",
		},
	}

	result := tool.ExecuteJob(context.Background(), job)
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
	if executor.lastContent != "generate report" {
		t.Errorf("expected content 'generate report', got %q", executor.lastContent)
	}
	if executor.lastSessionKey != "cron-agt1" {
		t.Errorf("expected session key 'cron-agt1', got %q", executor.lastSessionKey)
	}
}

func TestAddJob_SetsKind(t *testing.T) {
	tool, _ := newTestCronTool(t)
	tool.SetContext("telegram", "123")

	tests := []struct {
		name     string
		args     map[string]interface{}
		expected cron.JobType
	}{
		{
			name: "command job",
			args: map[string]interface{}{
				"action":        "add",
				"message":       "check disk",
				"command":       "df -h",
				"every_seconds": float64(3600),
			},
			expected: cron.JobTypeCommand,
		},
		{
			name: "deliver job",
			args: map[string]interface{}{
				"action":        "add",
				"message":       "reminder",
				"deliver":       true,
				"every_seconds": float64(3600),
			},
			expected: cron.JobTypeDeliver,
		},
		{
			name: "agent job",
			args: map[string]interface{}{
				"action":        "add",
				"message":       "complex task",
				"deliver":       false,
				"every_seconds": float64(3600),
			},
			expected: cron.JobTypeAgent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), tc.args)
			if result.IsError {
				t.Fatalf("Execute failed: %s", result.ForLLM)
			}
		})
	}
}
