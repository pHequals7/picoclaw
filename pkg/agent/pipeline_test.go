package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/failover"
	"github.com/sipeed/picoclaw/pkg/media"
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

// --- Parallel tool execution tests ---

// slowTool sleeps for a configured duration and records completion order.
type slowTool struct {
	name     string
	delay    time.Duration
	counter  *atomic.Int32
	orderIdx int32 // set after execution
}

func (t *slowTool) Name() string        { return t.name }
func (t *slowTool) Description() string  { return "slow tool for testing" }
func (t *slowTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *slowTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	time.Sleep(t.delay)
	if t.counter != nil {
		t.orderIdx = t.counter.Add(1)
	}
	return tools.SilentResult(fmt.Sprintf("%s done", t.name))
}

func TestExecuteToolsStage_ParallelExecution(t *testing.T) {
	// 3 tools each sleeping 50ms. Sequential = ~150ms. Parallel < 100ms.
	counter := &atomic.Int32{}
	registry := tools.NewToolRegistry()
	registry.Register(&slowTool{name: "slow_a", delay: 50 * time.Millisecond, counter: counter})
	registry.Register(&slowTool{name: "slow_b", delay: 50 * time.Millisecond, counter: counter})
	registry.Register(&slowTool{name: "slow_c", delay: 50 * time.Millisecond, counter: counter})

	stage := &ExecuteToolsStage{tools: registry, parallelTools: true}

	sc := &StageContext{
		Ctx: context.Background(),
		Response: &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{Name: "slow_a", ID: "t1", Arguments: map[string]interface{}{}},
				{Name: "slow_b", ID: "t2", Arguments: map[string]interface{}{}},
				{Name: "slow_c", ID: "t3", Arguments: map[string]interface{}{}},
			},
		},
		Messages:  []providers.Message{{Role: "user", Content: "go"}},
		PlanState: newExecutionPlanState(),
		Iteration: 1,
		Opts:      processOptions{},
	}

	start := time.Now()
	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 120*time.Millisecond {
		t.Errorf("parallel execution took %v, expected < 120ms", elapsed)
	}
	if counter.Load() != 3 {
		t.Errorf("expected 3 tools executed, got %d", counter.Load())
	}
}

func TestExecuteToolsStage_ParallelResultOrdering(t *testing.T) {
	// Tools complete in different times but messages must appear in call order.
	registry := tools.NewToolRegistry()
	registry.Register(&slowTool{name: "fast", delay: 10 * time.Millisecond})
	registry.Register(&slowTool{name: "medium", delay: 30 * time.Millisecond})
	registry.Register(&slowTool{name: "slow", delay: 50 * time.Millisecond})

	stage := &ExecuteToolsStage{tools: registry, parallelTools: true}

	sc := &StageContext{
		Ctx: context.Background(),
		Response: &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{Name: "slow", ID: "t1", Arguments: map[string]interface{}{}},
				{Name: "fast", ID: "t2", Arguments: map[string]interface{}{}},
				{Name: "medium", ID: "t3", Arguments: map[string]interface{}{}},
			},
		},
		Messages:  []providers.Message{{Role: "user", Content: "go"}},
		PlanState: newExecutionPlanState(),
		Iteration: 1,
		Opts:      processOptions{},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Messages: user, assistant, tool(t1), tool(t2), tool(t3)
	toolMsgs := []providers.Message{}
	for _, m := range sc.Messages {
		if m.Role == "tool" {
			toolMsgs = append(toolMsgs, m)
		}
	}
	if len(toolMsgs) != 3 {
		t.Fatalf("expected 3 tool result messages, got %d", len(toolMsgs))
	}
	// Verify order matches call order, not completion order
	expectedIDs := []string{"t1", "t2", "t3"}
	for i, m := range toolMsgs {
		if m.ToolCallID != expectedIDs[i] {
			t.Errorf("tool message %d: ToolCallID = %q, want %q", i, m.ToolCallID, expectedIDs[i])
		}
	}
}

func TestExecuteToolsStage_ParallelWithActionStream(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&slowTool{name: "tool_a", delay: 20 * time.Millisecond})
	registry.Register(&slowTool{name: "tool_b", delay: 20 * time.Millisecond})

	as := NewActionStream(config.VisibilityConfig{
		Enabled:          true,
		VerboseMode:      true, // Track all actions including internal
		UpdateIntervalMS: 10,
	}, func(summary string) {})

	stage := &ExecuteToolsStage{tools: registry, parallelTools: true}

	sc := &StageContext{
		Ctx: context.Background(),
		Response: &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{Name: "tool_a", ID: "a1", Arguments: map[string]interface{}{}},
				{Name: "tool_b", ID: "b1", Arguments: map[string]interface{}{}},
			},
		},
		Messages:  []providers.Message{{Role: "user", Content: "go"}},
		PlanState: newExecutionPlanState(),
		Iteration: 1,
		Opts:      processOptions{ActionStream: as},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both actions should have been tracked
	if as.GetActionCount() != 2 {
		t.Errorf("expected 2 tracked actions, got %d", as.GetActionCount())
	}
}

// errorTool returns an error result
type errorTool struct{}

func (t *errorTool) Name() string        { return "error_tool" }
func (t *errorTool) Description() string  { return "always errors" }
func (t *errorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *errorTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return tools.ErrorResult("tool failed").WithError(fmt.Errorf("tool failed"))
}

func TestExecuteToolsStage_ParallelToolError(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&slowTool{name: "good_tool", delay: 10 * time.Millisecond})
	registry.Register(&errorTool{})

	stage := &ExecuteToolsStage{tools: registry, parallelTools: true}

	sc := &StageContext{
		Ctx: context.Background(),
		Response: &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{Name: "good_tool", ID: "t1", Arguments: map[string]interface{}{}},
				{Name: "error_tool", ID: "t2", Arguments: map[string]interface{}{}},
			},
		},
		Messages:  []providers.Message{{Role: "user", Content: "go"}},
		PlanState: newExecutionPlanState(),
		Iteration: 1,
		Opts:      processOptions{},
	}

	// Should NOT return error - tool errors are captured in results
	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolMsgs := []providers.Message{}
	for _, m := range sc.Messages {
		if m.Role == "tool" {
			toolMsgs = append(toolMsgs, m)
		}
	}
	if len(toolMsgs) != 2 {
		t.Fatalf("expected 2 tool result messages, got %d", len(toolMsgs))
	}

	// First tool succeeds
	if toolMsgs[0].Content != "good_tool done" {
		t.Errorf("tool 1 content = %q, want 'good_tool done'", toolMsgs[0].Content)
	}
	// Second tool has error message
	if toolMsgs[1].Content != "tool failed" {
		t.Errorf("tool 2 content = %q, want 'tool failed'", toolMsgs[1].Content)
	}
}

// --- Resolve media stage tests ---

func TestResolveMediaStage_ResolvesRefs(t *testing.T) {
	dir := t.TempDir()
	// Write a file with PNG signature for MIME detection
	pngSig := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	testFile := fmt.Sprintf("%s/test.png", dir)
	if err := os.WriteFile(testFile, pngSig, 0644); err != nil {
		t.Fatal(err)
	}

	store := media.NewFileMediaStore()
	ref, err := store.Store(testFile, media.MediaMeta{
		OriginalName: "test.png",
		MIMEType:     "image/png",
		Scope:        "turn",
	})
	if err != nil {
		t.Fatal(err)
	}

	stage := &ResolveMediaStage{maxMediaSize: 1024 * 1024}
	sc := &StageContext{
		MediaStore: store,
		Messages: []providers.Message{
			{
				Role:    "user",
				Content: "look at this",
				Media: []providers.MediaImage{
					{MimeType: ref}, // ref goes in MimeType as carrier
				},
			},
		},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mi := sc.Messages[0].Media[0]
	if mi.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want image/png", mi.MimeType)
	}
	if mi.Base64Data == "" {
		t.Error("Base64Data is empty after resolve")
	}
}

func TestResolveMediaStage_SkipsWhenNoStore(t *testing.T) {
	stage := &ResolveMediaStage{}
	sc := &StageContext{
		MediaStore: nil,
		Messages: []providers.Message{
			{Role: "user", Content: "hi", Media: []providers.MediaImage{{MimeType: "media://foo"}}},
		},
	}

	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Media should be untouched
	if sc.Messages[0].Media[0].MimeType != "media://foo" {
		t.Error("media should be untouched when store is nil")
	}
}

func TestResolveMediaStage_LargeFileRejected(t *testing.T) {
	dir := t.TempDir()
	testFile := fmt.Sprintf("%s/big.dat", dir)
	if err := os.WriteFile(testFile, make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}

	store := media.NewFileMediaStore()
	ref, _ := store.Store(testFile, media.MediaMeta{Scope: "turn"})

	stage := &ResolveMediaStage{maxMediaSize: 50} // 50 bytes max
	sc := &StageContext{
		MediaStore: store,
		Messages: []providers.Message{
			{Role: "user", Media: []providers.MediaImage{{MimeType: ref}}},
		},
	}

	// Should not error (graceful skip), but image should not be resolved
	if err := stage.Execute(sc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.Messages[0].Media[0].Base64Data != "" {
		t.Error("oversized file should not be encoded")
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
