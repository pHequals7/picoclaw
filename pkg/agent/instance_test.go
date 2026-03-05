package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestAgentInstance_RunThroughPipeline(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{Content: "instance response"},
		},
	}

	registry := tools.NewToolRegistry()
	executor := &LLMExecutor{
		provider:      provider,
		model:         "test-model",
		maxIterations: 5,
		tools:         registry,
		parallelTools: true,
	}

	instance := &AgentInstance{
		ID:       "test-agent",
		Executor: executor,
		Tools:    registry,
		Config: AgentInstanceConfig{
			ID:    "test-agent",
			Model: "test-model",
		},
	}

	result := instance.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Hi from instance"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.FinalContent != "instance response" {
		t.Errorf("FinalContent = %q, want 'instance response'", result.FinalContent)
	}
}

func TestAgentInstance_RunWithToolCalls(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&mockCustomTool{})

	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "c1", Name: "mock_custom", Arguments: map[string]interface{}{}},
				},
			},
			{Content: "Done via instance"},
		},
	}

	executor := &LLMExecutor{
		provider:      provider,
		model:         "test-model",
		maxIterations: 5,
		tools:         registry,
		parallelTools: true,
		workspace:     t.TempDir(),
	}

	instance := &AgentInstance{
		ID:       "worker",
		Executor: executor,
		Tools:    registry,
	}

	result := instance.Run(context.Background(), []providers.Message{
		{Role: "user", Content: "Do work"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.FinalContent != "Done via instance" {
		t.Errorf("FinalContent = %q, want 'Done via instance'", result.FinalContent)
	}
	if result.Iterations != 2 {
		t.Errorf("Iterations = %d, want 2", result.Iterations)
	}
}
