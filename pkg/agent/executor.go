package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/failover"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/usage"
)

// UsageRecorder centralizes token accounting.
type UsageRecorder interface {
	Record(sessionKey, model, reason string, resp *providers.LLMResponse)
}

// SessionRecorder persists messages to session history.
type SessionRecorder interface {
	AddFullMessage(sessionKey string, msg providers.Message)
}

// FailoverRouter resolves which provider/model to use and handles rate limits.
type FailoverRouter interface {
	Enabled() bool
	ResolveRoute() (failover.Route, error)
	OnLLMRateLimited(model string, err error) failover.SwitchEvent
	OnLLMSuccess(model string)
}

// PlanGenerator generates execution plan bullets from tool calls.
type PlanGenerator interface {
	Generate(ctx context.Context, opts processOptions, model string,
		provider providers.LLMProvider, toolCalls []providers.ToolCall) ([]string, string)
}

// LLMExecutor handles the iterative LLM call + tool execution loop.
type LLMExecutor struct {
	provider      providers.LLMProvider
	model         string
	maxIterations int
	tools         *tools.ToolRegistry
	activeToolSet *tools.ActiveToolSet // nil = send all tools (no search)
	failover      FailoverRouter       // nil = no failover
	usage         UsageRecorder        // nil = no usage tracking
	sessions      SessionRecorder      // nil = no session recording
	publisher     *bus.MessageBus      // nil = no progress publishing
	planner       PlanGenerator        // nil = no plan generation
	workspace     string
	parallelTools      bool              // Execute tool calls concurrently
	maxToolResultChars int               // Max chars to persist in session history (0 = unlimited)
	mediaStore         media.MediaStore  // nil = no media pipeline
	maxMediaSize       int64             // max bytes per media file; 0 = no limit
	notifySwitch  func(channel, chatID string, event failover.SwitchEvent)
}

// IterationResult contains the outcome of an LLM iteration loop.
type IterationResult struct {
	FinalContent string
	Iterations   int
	Err          error
}

// Run executes the LLM call loop with tool handling via pipeline stages.
func (ex *LLMExecutor) Run(ctx context.Context, messages []providers.Message, opts processOptions) IterationResult {
	// Reset deferred tools for each new conversation turn
	if ex.activeToolSet != nil {
		ex.activeToolSet.Reset()
	}

	pipeline := ex.buildPipeline()
	sc := &StageContext{
		Ctx:           ctx,
		Messages:      messages,
		Opts:          opts,
		PlanState:     newExecutionPlanState(),
		ActiveToolSet: ex.activeToolSet,
		MediaStore:    ex.mediaStore,
	}

	for i := 0; i < ex.maxIterations; i++ {
		sc.Iteration = i + 1
		sc.Done = false

		logger.DebugCF("agent", "LLM iteration",
			map[string]interface{}{
				"iteration": sc.Iteration,
				"max":       ex.maxIterations,
			})

		if err := pipeline.RunIteration(sc); err != nil {
			return IterationResult{Err: err, Iterations: sc.Iteration}
		}
		if sc.Done {
			break
		}
	}

	// Force final update if visibility enabled
	if opts.ActionStream != nil {
		opts.ActionStream.ForceUpdate()
	}

	return IterationResult{FinalContent: sc.FinalContent, Iterations: sc.Iteration}
}

// RunSubagent implements tools.SubagentExecutor, allowing the pipeline-based
// executor to be used for subagent execution instead of RunToolLoop.
func (ex *LLMExecutor) RunSubagent(ctx context.Context, messages []providers.Message, channel, chatID string) (string, int, error) {
	opts := processOptions{
		SessionKey:  fmt.Sprintf("subagent:%s:%s", channel, chatID),
		Channel:     channel,
		ChatID:      chatID,
		SendResponse: false,
	}
	result := ex.Run(ctx, messages, opts)
	return result.FinalContent, result.Iterations, result.Err
}

// --- Adapter implementations ---

// usageRecorderAdapter wraps *usage.Store to implement UsageRecorder.
type usageRecorderAdapter struct {
	store *usage.Store
}

func (a *usageRecorderAdapter) Record(sessionKey, model, reason string, resp *providers.LLMResponse) {
	usageKnown := resp.Usage != nil
	promptTokens := 0
	completionTokens := 0
	totalTokens := 0
	if usageKnown {
		promptTokens = resp.Usage.PromptTokens
		completionTokens = resp.Usage.CompletionTokens
		totalTokens = resp.Usage.TotalTokens
	}
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	a.store.Add(usage.Record{
		Timestamp:        time.Now().UTC(),
		SessionKey:       sessionKey,
		DayKey:           time.Now().UTC().Format("2006-01-02"),
		Provider:         providerFromModel(model),
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		UsageKnown:       usageKnown,
		Reason:           reason,
	})
}

// sessionRecorderAdapter wraps *session.SessionManager to implement SessionRecorder.
type sessionRecorderAdapter struct {
	sessions interface {
		AddFullMessage(sessionKey string, msg providers.Message)
	}
}

func (a *sessionRecorderAdapter) AddFullMessage(sessionKey string, msg providers.Message) {
	a.sessions.AddFullMessage(sessionKey, msg)
}

// plannerAdapter wraps the existing AgentLoop.generateExecutionPlanBullets.
type plannerAdapter struct {
	al *AgentLoop
}

func (a *plannerAdapter) Generate(ctx context.Context, opts processOptions, model string,
	provider providers.LLMProvider, toolCalls []providers.ToolCall) ([]string, string) {
	return a.al.generateExecutionPlanBullets(ctx, opts, model, provider, toolCalls)
}
