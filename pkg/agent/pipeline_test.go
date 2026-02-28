package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sipeed/picoclaw/pkg/failover"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// --- Per-stage unit tests ---

func TestResolveRouteStage_NoFailover(t *testing.T) {
	provider := &mockLLMProvider{}
	stage := &ResolveRouteStage{
		defaultProvider: provider,
		defaultModel:    "default-model",
		failover:        nil,
	}

	sc := &StageContext{}
	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.ActiveModel != "default-model" {
		t.Errorf("ActiveModel = %q, want default-model", sc.ActiveModel)
	}
	if sc.ActiveProvider != provider {
		t.Error("ActiveProvider not set to default")
	}
	if sc.SwitchEpoch != 0 {
		t.Errorf("SwitchEpoch = %d, want 0", sc.SwitchEpoch)
	}
}

func TestResolveRouteStage_WithFailover(t *testing.T) {
	fallbackProvider := &mockLLMProvider{}
	stage := &ResolveRouteStage{
		defaultProvider: &mockLLMProvider{},
		defaultModel:    "default-model",
		failover: &mockFailoverRouter{
			enabled: true,
			route: failover.Route{
				Provider:    fallbackProvider,
				Model:       "fallback-model",
				SwitchEpoch: 42,
			},
		},
	}

	sc := &StageContext{}
	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.ActiveModel != "fallback-model" {
		t.Errorf("ActiveModel = %q, want fallback-model", sc.ActiveModel)
	}
	if sc.ActiveProvider != fallbackProvider {
		t.Error("ActiveProvider not set to fallback")
	}
	if sc.SwitchEpoch != 42 {
		t.Errorf("SwitchEpoch = %d, want 42", sc.SwitchEpoch)
	}
}

func TestCallLLMStage_Success(t *testing.T) {
	provider := &mockLLMProvider{
		responses: []*providers.LLMResponse{{Content: "hello"}},
	}

	registry := tools.NewToolRegistry()
	stage := &CallLLMStage{tools: registry}

	sc := &StageContext{
		Ctx:            context.Background(),
		Messages:       []providers.Message{{Role: "user", Content: "hi"}},
		ActiveProvider: provider,
		ActiveModel:    "test-model",
		Iteration:      1,
		Opts:           processOptions{},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.Response == nil {
		t.Fatal("Response is nil")
	}
	if sc.Response.Content != "hello" {
		t.Errorf("Response.Content = %q, want hello", sc.Response.Content)
	}
}

func TestCallLLMStage_RateLimit(t *testing.T) {
	fallbackProvider := &mockLLMProvider{
		responses: []*providers.LLMResponse{{Content: "fallback response"}},
	}

	stage := &CallLLMStage{
		failover: &mockFailoverRouter{
			enabled: true,
			switchEvent: failover.SwitchEvent{
				Switched:  true,
				FromModel: "primary",
				ToModel:   "fallback",
				Reason:    "rate limited",
			},
			route: failover.Route{
				Provider: fallbackProvider,
				Model:    "fallback",
			},
		},
		tools: tools.NewToolRegistry(),
	}

	rateLimitProvider := &mockLLMProvider{
		errors: []error{&providers.RateLimitError{StatusCode: 429, Body: "rate limited"}},
	}

	sc := &StageContext{
		Ctx:            context.Background(),
		Messages:       []providers.Message{{Role: "user", Content: "hi"}},
		ActiveProvider: rateLimitProvider,
		ActiveModel:    "primary",
		Iteration:      1,
		Opts:           processOptions{},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.Response == nil {
		t.Fatal("Response is nil after failover")
	}
	if sc.Response.Content != "fallback response" {
		t.Errorf("Response.Content = %q, want fallback response", sc.Response.Content)
	}
	if sc.ActiveModel != "fallback" {
		t.Errorf("ActiveModel = %q, want fallback", sc.ActiveModel)
	}
}

func TestCallLLMStage_FatalError(t *testing.T) {
	fatalErr := fmt.Errorf("fatal error")
	stage := &CallLLMStage{tools: tools.NewToolRegistry()}

	sc := &StageContext{
		Ctx:            context.Background(),
		Messages:       []providers.Message{{Role: "user", Content: "hi"}},
		ActiveProvider: &mockLLMProvider{errors: []error{fatalErr}},
		ActiveModel:    "test-model",
		Iteration:      1,
		Opts:           processOptions{},
	}

	err := stage.Execute(sc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, fatalErr) {
		t.Errorf("error should wrap original: %v", err)
	}
}

func TestRecordUsageStage_Records(t *testing.T) {
	recorder := &mockUsageRecorder{}
	stage := &RecordUsageStage{usage: recorder}

	sc := &StageContext{
		ActiveModel: "test-model",
		Opts:        processOptions{SessionKey: "sess1"},
		Response: &providers.LLMResponse{
			Content:      "hi",
			FinishReason: "stop",
			Usage:        &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 5},
		},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recorder.records) != 1 {
		t.Errorf("expected 1 record, got %d", len(recorder.records))
	}
	if recorder.records[0].reason != "stop" {
		t.Errorf("reason = %q, want stop", recorder.records[0].reason)
	}
}

func TestRecordUsageStage_NilRecorder(t *testing.T) {
	stage := &RecordUsageStage{usage: nil}
	sc := &StageContext{
		Response: &providers.LLMResponse{Content: "hi"},
	}
	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error with nil recorder: %v", err)
	}
}

func TestCheckCompletionStage_NoTools(t *testing.T) {
	stage := &CheckCompletionStage{}
	sc := &StageContext{
		Response:  &providers.LLMResponse{Content: "final answer"},
		Iteration: 1,
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sc.Done {
		t.Error("expected Done = true")
	}
	if sc.FinalContent != "final answer" {
		t.Errorf("FinalContent = %q, want final answer", sc.FinalContent)
	}
}

func TestCheckCompletionStage_HasTools(t *testing.T) {
	stage := &CheckCompletionStage{}
	sc := &StageContext{
		Response: &providers.LLMResponse{
			Content: "thinking...",
			ToolCalls: []providers.ToolCall{
				{Name: "shell", ID: "1"},
			},
		},
		Iteration: 1,
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.Done {
		t.Error("expected Done = false when tool calls present")
	}
}

func TestAnnouncePlanStage_FirstBatch(t *testing.T) {
	stage := &AnnouncePlanStage{
		workspace: t.TempDir(),
	}

	sc := &StageContext{
		Ctx: context.Background(),
		Response: &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{Name: "shell", ID: "1"},
			},
		},
		PlanState:   newExecutionPlanState(),
		ActiveModel: "test-model",
		Iteration:   1,
		Opts:        processOptions{SessionKey: "test"},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sc.PlanState.Announced {
		t.Error("expected plan to be announced")
	}
	if len(sc.PlanState.Bullets) == 0 {
		t.Error("expected non-empty plan bullets")
	}
}

func TestAnnouncePlanStage_AlreadyAnnounced(t *testing.T) {
	stage := &AnnouncePlanStage{workspace: t.TempDir()}

	planState := newExecutionPlanState()
	planState.Announced = true
	planState.Bullets = []string{"step 1"}

	sc := &StageContext{
		Response: &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{Name: "shell", ID: "1"},
			},
		},
		PlanState: planState,
		Iteration: 2,
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sc.PlanState.Bullets) != 1 {
		t.Errorf("expected 1 bullet, got %d", len(sc.PlanState.Bullets))
	}
}

func TestExecuteToolsStage_SingleTool(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&mockCustomTool{})
	stage := &ExecuteToolsStage{tools: registry}

	sc := &StageContext{
		Ctx: context.Background(),
		Response: &providers.LLMResponse{
			Content: "",
			ToolCalls: []providers.ToolCall{
				{Name: "mock_custom", ID: "tc1", Arguments: map[string]interface{}{}},
			},
		},
		Messages:  []providers.Message{{Role: "user", Content: "hi"}},
		PlanState: newExecutionPlanState(),
		Iteration: 1,
		Opts:      processOptions{},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have appended assistant msg + tool result msg
	if len(sc.Messages) != 3 {
		t.Errorf("expected 3 messages (user + assistant + tool result), got %d", len(sc.Messages))
	}
	if sc.Messages[1].Role != "assistant" {
		t.Errorf("messages[1].Role = %q, want assistant", sc.Messages[1].Role)
	}
	if sc.Messages[2].Role != "tool" {
		t.Errorf("messages[2].Role = %q, want tool", sc.Messages[2].Role)
	}
}

// --- Pipeline integration tests ---

func TestPipeline_FullIteration(t *testing.T) {
	var executedStages []string

	pipeline := &Pipeline{
		stages: []Stage{
			&trackingStage{name: "s1", executed: &executedStages},
			&trackingStage{name: "s2", executed: &executedStages},
			&trackingStage{name: "s3", executed: &executedStages},
		},
	}

	sc := &StageContext{}
	if err := pipeline.RunIteration(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(executedStages) != 3 {
		t.Errorf("expected 3 stages executed, got %d", len(executedStages))
	}
}

func TestPipeline_StopsOnDone(t *testing.T) {
	var executedStages []string

	pipeline := &Pipeline{
		stages: []Stage{
			&trackingStage{name: "s1", executed: &executedStages},
			&trackingStage{name: "s2", executed: &executedStages, setDone: true},
			&trackingStage{name: "s3", executed: &executedStages},
		},
	}

	sc := &StageContext{}
	if err := pipeline.RunIteration(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(executedStages) != 2 {
		t.Errorf("expected 2 stages executed (stopped at s2), got %d: %v", len(executedStages), executedStages)
	}
}

func TestPipeline_StageError(t *testing.T) {
	var executedStages []string
	expectedErr := errors.New("stage failed")

	pipeline := &Pipeline{
		stages: []Stage{
			&trackingStage{name: "s1", executed: &executedStages},
			&trackingStage{name: "s2", executed: &executedStages, returnErr: expectedErr},
			&trackingStage{name: "s3", executed: &executedStages},
		},
	}

	sc := &StageContext{}
	err := pipeline.RunIteration(sc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("error should wrap original: %v", err)
	}
	if len(executedStages) != 2 {
		t.Errorf("expected 2 stages executed (s1 + s2 that errored), got %d", len(executedStages))
	}
}

// --- Test helpers ---

type trackingStage struct {
	name      string
	executed  *[]string
	setDone   bool
	returnErr error
}

func (s *trackingStage) Name() string { return s.name }
func (s *trackingStage) Execute(sc *StageContext) error {
	*s.executed = append(*s.executed, s.name)
	if s.setDone {
		sc.Done = true
	}
	return s.returnErr
}
