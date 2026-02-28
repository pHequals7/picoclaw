package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/sipeed/picoclaw/pkg/failover"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// --- Mock implementations for executor tests ---

type mockLLMProvider struct {
	responses []*providers.LLMResponse
	errors    []error
	callCount int
}

func (m *mockLLMProvider) Chat(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	idx := m.callCount
	m.callCount++
	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return &providers.LLMResponse{Content: "default response"}, nil
}

func (m *mockLLMProvider) GetDefaultModel() string { return "mock-model" }

type mockFailoverRouter struct {
	enabled      bool
	route        failover.Route
	switchEvent  failover.SwitchEvent
	successCount int
}

func (m *mockFailoverRouter) Enabled() bool { return m.enabled }
func (m *mockFailoverRouter) ResolveRoute() (failover.Route, error) {
	return m.route, nil
}
func (m *mockFailoverRouter) OnLLMRateLimited(model string, err error) failover.SwitchEvent {
	return m.switchEvent
}
func (m *mockFailoverRouter) OnLLMSuccess(model string) {
	m.successCount++
}

type mockUsageRecorder struct {
	records []usageRecord
}

type usageRecord struct {
	sessionKey string
	model      string
	reason     string
}

func (m *mockUsageRecorder) Record(sessionKey, model, reason string, resp *providers.LLMResponse) {
	m.records = append(m.records, usageRecord{sessionKey: sessionKey, model: model, reason: reason})
}

type mockSessionRecorder struct {
	messages []providers.Message
}

func (m *mockSessionRecorder) AddFullMessage(sessionKey string, msg providers.Message) {
	m.messages = append(m.messages, msg)
}

type mockPlanGenerator struct {
	called bool
}

func (m *mockPlanGenerator) Generate(ctx context.Context, opts processOptions, model string,
	provider providers.LLMProvider, toolCalls []providers.ToolCall) ([]string, string) {
	m.called = true
	return []string{"Step 1: Do something"}, model
}

func newTestExecutor(provider providers.LLMProvider) *LLMExecutor {
	registry := tools.NewToolRegistry()
	return &LLMExecutor{
		provider:      provider,
		model:         "test-model",
		maxIterations: 10,
		tools:         registry,
		workspace:     "",
	}
}

// --- Tests ---

func TestLLMExecutor_DirectResponse(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{Content: "Hello, world!"},
		},
	}
	ex := newTestExecutor(provider)

	result := ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hi"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.FinalContent != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", result.FinalContent)
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
}

func TestLLMExecutor_ToolCallLoop(t *testing.T) {
	// Register a simple tool
	registry := tools.NewToolRegistry()
	registry.Register(&mockCustomTool{})

	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{
				Content: "",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "mock_custom", Arguments: map[string]interface{}{}},
				},
			},
			{Content: "Done after tool call"},
		},
	}

	ex := &LLMExecutor{
		provider:      provider,
		model:         "test-model",
		maxIterations: 10,
		tools:         registry,
		workspace:     t.TempDir(),
	}

	result := ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Do something"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.FinalContent != "Done after tool call" {
		t.Errorf("expected 'Done after tool call', got %q", result.FinalContent)
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
}

func TestLLMExecutor_MaxIterationsExceeded(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{ToolCalls: []providers.ToolCall{{ID: "c1", Name: "mock_custom", Arguments: map[string]interface{}{}}}},
			{ToolCalls: []providers.ToolCall{{ID: "c2", Name: "mock_custom", Arguments: map[string]interface{}{}}}},
			{ToolCalls: []providers.ToolCall{{ID: "c3", Name: "mock_custom", Arguments: map[string]interface{}{}}}},
		},
	}

	registry := tools.NewToolRegistry()
	registry.Register(&mockCustomTool{})

	ex := &LLMExecutor{
		provider:      provider,
		model:         "test-model",
		maxIterations: 3,
		tools:         registry,
		workspace:     t.TempDir(),
	}

	result := ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Loop forever"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Iterations != 3 {
		t.Errorf("expected 3 iterations (max), got %d", result.Iterations)
	}
}

func TestLLMExecutor_LLMError(t *testing.T) {
	provider := &mockLLMProvider{
		errors: []error{fmt.Errorf("model overloaded")},
	}
	ex := newTestExecutor(provider)

	result := ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Hi"},
	}, processOptions{SessionKey: "test"})

	if result.Err == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
}

func TestLLMExecutor_RateLimitWithFailover(t *testing.T) {
	fallbackProvider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{Content: "Fallback response"},
		},
	}

	primaryProvider := &mockLLMProvider{
		errors: []error{&providers.RateLimitError{StatusCode: 429, Body: "rate limited"}},
	}

	fr := &mockFailoverRouter{
		enabled: true,
		route: failover.Route{
			Model:    "fallback-model",
			Provider: fallbackProvider,
		},
		switchEvent: failover.SwitchEvent{
			Switched:  true,
			FromModel: "primary-model",
			ToModel:   "fallback-model",
			Reason:    "rate_limit",
		},
	}

	ex := &LLMExecutor{
		provider:      primaryProvider,
		model:         "primary-model",
		maxIterations: 10,
		tools:         tools.NewToolRegistry(),
		failover:      fr,
	}

	result := ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Hi"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.FinalContent != "Fallback response" {
		t.Errorf("expected fallback response, got %q", result.FinalContent)
	}
	if fr.successCount != 1 {
		t.Errorf("expected 1 success call, got %d", fr.successCount)
	}
}

func TestLLMExecutor_UsageRecording(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{
				Content:      "Response",
				FinishReason: "stop",
				Usage:        &providers.UsageInfo{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			},
		},
	}

	recorder := &mockUsageRecorder{}
	ex := newTestExecutor(provider)
	ex.usage = recorder

	ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Hi"},
	}, processOptions{SessionKey: "my-session"})

	if len(recorder.records) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(recorder.records))
	}
	rec := recorder.records[0]
	if rec.sessionKey != "my-session" {
		t.Errorf("expected session key 'my-session', got %q", rec.sessionKey)
	}
	if rec.model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", rec.model)
	}
	if rec.reason != "stop" {
		t.Errorf("expected reason 'stop', got %q", rec.reason)
	}
}

func TestLLMExecutor_NilDependencies(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{Content: "OK"},
		},
	}

	// All optional deps are nil
	ex := &LLMExecutor{
		provider:      provider,
		model:         "test-model",
		maxIterations: 10,
		tools:         tools.NewToolRegistry(),
		failover:      nil,
		usage:         nil,
		sessions:      nil,
		publisher:     nil,
		planner:       nil,
	}

	result := ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Hi"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error with nil deps: %v", result.Err)
	}
	if result.FinalContent != "OK" {
		t.Errorf("expected 'OK', got %q", result.FinalContent)
	}
}

func TestLLMExecutor_PlanGeneration(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&mockCustomTool{})

	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "c1", Name: "mock_custom", Arguments: map[string]interface{}{}},
				},
			},
			{Content: "Done"},
		},
	}

	planner := &mockPlanGenerator{}
	ex := &LLMExecutor{
		provider:      provider,
		model:         "test-model",
		maxIterations: 10,
		tools:         registry,
		planner:       planner,
		workspace:     t.TempDir(),
	}

	result := ex.Run(context.Background(), []providers.Message{
		{Role: "system", Content: "System"},
		{Role: "user", Content: "Do something complex"},
	}, processOptions{SessionKey: "test"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !planner.called {
		t.Error("expected plan generator to be called on first tool batch")
	}
	if result.FinalContent != "Done" {
		t.Errorf("expected 'Done', got %q", result.FinalContent)
	}
}
