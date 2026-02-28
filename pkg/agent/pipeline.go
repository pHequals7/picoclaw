package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/failover"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// StageContext is mutable state threaded through pipeline stages.
type StageContext struct {
	Ctx            context.Context
	Messages       []providers.Message
	Opts           processOptions
	ActiveProvider providers.LLMProvider
	ActiveModel    string
	SwitchEpoch    int64
	Response       *providers.LLMResponse
	PlanState      *executionPlanState
	ToolDefs       []providers.ToolDefinition
	ActiveToolSet  *tools.ActiveToolSet
	FinalContent   string
	Iteration      int
	Done           bool
}

// Stage is a single step in the LLM iteration pipeline.
type Stage interface {
	Name() string
	Execute(sc *StageContext) error
}

// Pipeline runs a sequence of stages.
type Pipeline struct {
	stages []Stage
}

// RunIteration executes all stages in sequence, stopping early if Done or error.
func (p *Pipeline) RunIteration(sc *StageContext) error {
	for _, s := range p.stages {
		if sc.Done {
			break
		}
		if err := s.Execute(sc); err != nil {
			return fmt.Errorf("stage %s: %w", s.Name(), err)
		}
	}
	return nil
}

// --- Stage implementations ---

// ResolveRouteStage sets the active provider/model, consulting failover if available.
type ResolveRouteStage struct {
	defaultProvider providers.LLMProvider
	defaultModel    string
	failover        FailoverRouter
}

func (s *ResolveRouteStage) Name() string { return "resolve_route" }

func (s *ResolveRouteStage) Execute(sc *StageContext) error {
	sc.ActiveProvider = s.defaultProvider
	sc.ActiveModel = s.defaultModel
	sc.SwitchEpoch = 0

	if s.failover != nil && s.failover.Enabled() {
		route, err := s.failover.ResolveRoute()
		if err != nil {
			return fmt.Errorf("resolve failover route: %w", err)
		}
		sc.ActiveProvider = route.Provider
		sc.ActiveModel = route.Model
		sc.SwitchEpoch = route.SwitchEpoch
	}
	return nil
}

// CallLLMStage calls the LLM and handles rate limit failover retry.
type CallLLMStage struct {
	failover     FailoverRouter
	tools        *tools.ToolRegistry
	notifySwitch func(channel, chatID string, event failover.SwitchEvent)
}

func (s *CallLLMStage) Name() string { return "call_llm" }

func (s *CallLLMStage) Execute(sc *StageContext) error {
	if sc.ActiveToolSet != nil {
		sc.ToolDefs = sc.ActiveToolSet.ProviderDefs()
	} else {
		sc.ToolDefs = s.tools.ToProviderDefs()
	}

	logger.DebugCF("agent", "LLM request",
		map[string]interface{}{
			"iteration":      sc.Iteration,
			"model":          sc.ActiveModel,
			"messages_count": len(sc.Messages),
			"tools_count":    len(sc.ToolDefs),
		})

	logger.DebugCF("agent", "Full LLM request",
		map[string]interface{}{
			"iteration":     sc.Iteration,
			"messages_json": formatMessagesForLog(sc.Messages),
			"tools_json":    formatToolsForLog(sc.ToolDefs),
		})

	response, err := sc.ActiveProvider.Chat(sc.Ctx, sc.Messages, sc.ToolDefs, sc.ActiveModel, map[string]interface{}{
		"max_tokens":  8192,
		"temperature": 0.7,
	})

	if err != nil {
		var rateLimitErr *providers.RateLimitError
		if s.failover != nil && s.failover.Enabled() && errors.As(err, &rateLimitErr) {
			switchEvent := s.failover.OnLLMRateLimited(sc.ActiveModel, err)
			logger.WarnCF("agent", "Failover switch evaluation",
				map[string]interface{}{
					"iteration":      sc.Iteration,
					"from_model":     switchEvent.FromModel,
					"to_model":       switchEvent.ToModel,
					"reason":         switchEvent.Reason,
					"switched":       switchEvent.Switched,
					"status_code":    rateLimitErr.StatusCode,
					"switch_epoch":   sc.SwitchEpoch,
					"correlation_id": sc.Opts.CorrelationID,
				})

			if switchEvent.Switched {
				if s.notifySwitch != nil {
					s.notifySwitch(sc.Opts.Channel, sc.Opts.ChatID, switchEvent)
				}
				retryRoute, routeErr := s.failover.ResolveRoute()
				if routeErr != nil {
					return fmt.Errorf("resolve failover retry route: %w", routeErr)
				}
				sc.ActiveProvider = retryRoute.Provider
				sc.ActiveModel = retryRoute.Model
				sc.SwitchEpoch = retryRoute.SwitchEpoch

				response, err = sc.ActiveProvider.Chat(sc.Ctx, sc.Messages, sc.ToolDefs, sc.ActiveModel, map[string]interface{}{
					"max_tokens":  8192,
					"temperature": 0.7,
				})
			}
		}

		if err != nil {
			logger.ErrorCF("agent", "LLM call failed",
				map[string]interface{}{
					"iteration":      sc.Iteration,
					"error":          err.Error(),
					"model":          sc.ActiveModel,
					"switch_epoch":   sc.SwitchEpoch,
					"correlation_id": sc.Opts.CorrelationID,
				})
			return fmt.Errorf("LLM call failed: %w", err)
		}
	}

	if s.failover != nil && s.failover.Enabled() {
		s.failover.OnLLMSuccess(sc.ActiveModel)
	}

	sc.Response = response
	return nil
}

// RecordUsageStage records token usage.
type RecordUsageStage struct {
	usage UsageRecorder
}

func (s *RecordUsageStage) Name() string { return "record_usage" }

func (s *RecordUsageStage) Execute(sc *StageContext) error {
	if s.usage == nil {
		return nil
	}
	reason := strings.TrimSpace(sc.Response.FinishReason)
	if reason == "" {
		reason = "normal_call"
	}
	s.usage.Record(sc.Opts.SessionKey, sc.ActiveModel, reason, sc.Response)
	return nil
}

// CheckCompletionStage checks if the LLM returned without tool calls.
type CheckCompletionStage struct{}

func (s *CheckCompletionStage) Name() string { return "check_completion" }

func (s *CheckCompletionStage) Execute(sc *StageContext) error {
	if len(sc.Response.ToolCalls) == 0 {
		sc.FinalContent = sc.Response.Content
		sc.Done = true
		logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
			map[string]interface{}{
				"iteration":     sc.Iteration,
				"content_chars": len(sc.FinalContent),
			})
	}
	return nil
}

// AnnouncePlanStage generates and publishes the execution plan on the first tool-call batch.
type AnnouncePlanStage struct {
	planner   PlanGenerator
	publisher *bus.MessageBus
	workspace string
}

func (s *AnnouncePlanStage) Name() string { return "announce_plan" }

func (s *AnnouncePlanStage) Execute(sc *StageContext) error {
	if sc.PlanState.Announced {
		return nil
	}

	toolNames := make([]string, 0, len(sc.Response.ToolCalls))
	for _, tc := range sc.Response.ToolCalls {
		toolNames = append(toolNames, tc.Name)
	}
	logger.InfoCF("agent", "LLM requested tool calls",
		map[string]interface{}{
			"tools":          toolNames,
			"count":          len(sc.Response.ToolCalls),
			"iteration":      sc.Iteration,
			"correlation_id": sc.Opts.CorrelationID,
		})

	planModel := sc.ActiveModel
	if s.planner != nil {
		sc.PlanState.Bullets, planModel = s.planner.Generate(sc.Ctx, sc.Opts, sc.ActiveModel, sc.ActiveProvider, sc.Response.ToolCalls)
	} else {
		sc.PlanState.Bullets = buildExecutionPlanBullets(sc.Response.ToolCalls)
	}
	sc.PlanState.absorbToolCalls(sc.Response.ToolCalls)
	sc.PlanState.Announced = true

	planPath, planErr := writeExecutionPlanFile(s.workspace, sc.PlanState.Bullets, planFileMetadata{
		SessionKey:    sc.Opts.SessionKey,
		CorrelationID: sc.Opts.CorrelationID,
		Model:         planModel,
	}, time.Now())
	if planErr != nil {
		logger.WarnCF("agent", "Failed to persist execution plan file",
			map[string]interface{}{
				"error":          planErr.Error(),
				"session_key":    sc.Opts.SessionKey,
				"correlation_id": sc.Opts.CorrelationID,
			})
	} else {
		logger.InfoCF("agent", "Execution plan file created",
			map[string]interface{}{
				"path":           planPath,
				"bullets":        len(sc.PlanState.Bullets),
				"session_key":    sc.Opts.SessionKey,
				"correlation_id": sc.Opts.CorrelationID,
			})
	}

	// Only publish the plan to the user when there are 3+ steps.
	// Simple 1-2 tool tasks don't need a visible plan announcement.
	if len(sc.PlanState.Bullets) >= 3 && shouldPublishProgress(sc.Opts) && s.publisher != nil {
		planMsg := formatExecutionPlanProgressWithArtifact(sc.PlanState.Bullets, planPath)
		s.publisher.PublishOutbound(bus.OutboundMessage{
			Channel:          sc.Opts.Channel,
			ChatID:           sc.Opts.ChatID,
			Content:          planMsg,
			IsProgressUpdate: false,
		})
		s.publisher.PublishOutbound(bus.OutboundMessage{
			Channel:          sc.Opts.Channel,
			ChatID:           sc.Opts.ChatID,
			Content:          "Working... 🔧",
			IsProgressUpdate: true,
		})
	}

	sc.Messages = append(sc.Messages, providers.Message{
		Role:    "system",
		Content: formatPlanContextMessage(sc.PlanState.Bullets),
	})

	return nil
}

// ExecuteToolsStage executes tool calls and appends results to messages.
type ExecuteToolsStage struct {
	tools     *tools.ToolRegistry
	sessions  SessionRecorder
	publisher *bus.MessageBus
}

func (s *ExecuteToolsStage) Name() string { return "execute_tools" }

func (s *ExecuteToolsStage) Execute(sc *StageContext) error {
	// Build assistant message with tool calls
	assistantMsg := providers.Message{
		Role:    "assistant",
		Content: sc.Response.Content,
	}
	for _, tc := range sc.Response.ToolCalls {
		argumentsJSON, _ := json.Marshal(tc.Arguments)
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      tc.Name,
				Arguments: string(argumentsJSON),
			},
		})
	}
	sc.Messages = append(sc.Messages, assistantMsg)

	if s.sessions != nil {
		s.sessions.AddFullMessage(sc.Opts.SessionKey, assistantMsg)
	}

	// Execute each tool call
	for _, tc := range sc.Response.ToolCalls {
		// Out-of-plan tool update
		tcName := strings.TrimSpace(tc.Name)
		if tcName == "" && tc.Function != nil {
			tcName = strings.TrimSpace(tc.Function.Name)
		}
		if sc.PlanState.Announced && tcName != "" && !sc.PlanState.isAllowedTool(tcName) {
			updateStep := summarizeToolCallForPlan(tc)
			sc.PlanState.Bullets = append(sc.PlanState.Bullets, updateStep)
			sc.PlanState.Allowed[tcName] = struct{}{}

			updateMsg := formatPlanUpdateProgress(updateStep)
			if shouldPublishProgress(sc.Opts) && s.publisher != nil {
				s.publisher.PublishOutbound(bus.OutboundMessage{
					Channel:          sc.Opts.Channel,
					ChatID:           sc.Opts.ChatID,
					Content:          updateMsg,
					IsProgressUpdate: true,
				})
			}

			sc.Messages = append(sc.Messages, providers.Message{
				Role:    "system",
				Content: formatPlanContextMessage(sc.PlanState.Bullets),
			})
		}

		argsJSON, _ := json.Marshal(tc.Arguments)
		argsPreview := utils.Truncate(string(argsJSON), 200)
		logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
			map[string]interface{}{
				"tool":           tc.Name,
				"iteration":      sc.Iteration,
				"correlation_id": sc.Opts.CorrelationID,
			})

		var actionID string
		if sc.Opts.ActionStream != nil {
			actionID = sc.Opts.ActionStream.StartAction(tc.Name, tc.Arguments)
		}

		asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
			if !result.Silent && result.ForUser != "" {
				logger.InfoCF("agent", "Async tool completed, agent will handle notification",
					map[string]interface{}{
						"tool":        tc.Name,
						"content_len": len(result.ForUser),
					})
			}
		}

		toolResult := s.tools.ExecuteWithContext(sc.Ctx, tc.Name, tc.Arguments, sc.Opts.Channel, sc.Opts.ChatID, asyncCallback)

		if sc.Opts.ActionStream != nil && actionID != "" {
			resultContent := toolResult.ForUser
			if resultContent == "" {
				resultContent = toolResult.ForLLM
			}
			sc.Opts.ActionStream.CompleteAction(actionID, resultContent, toolResult.Err)
		}

		if !toolResult.Silent && toolResult.ForUser != "" && sc.Opts.SendResponse && s.publisher != nil {
			s.publisher.PublishOutbound(bus.OutboundMessage{
				Channel: sc.Opts.Channel,
				ChatID:  sc.Opts.ChatID,
				Content: toolResult.ForUser,
			})
		}

		contentForLLM := toolResult.ForLLM
		if contentForLLM == "" && toolResult.Err != nil {
			contentForLLM = toolResult.Err.Error()
		}

		toolResultMsg := providers.Message{
			Role:       "tool",
			Content:    contentForLLM,
			ToolCallID: tc.ID,
		}
		sc.Messages = append(sc.Messages, toolResultMsg)

		if s.sessions != nil {
			s.sessions.AddFullMessage(sc.Opts.SessionKey, toolResultMsg)
		}
	}

	return nil
}

// buildPipeline constructs the standard pipeline for the LLMExecutor.
func (ex *LLMExecutor) buildPipeline() *Pipeline {
	return &Pipeline{
		stages: []Stage{
			&ResolveRouteStage{
				defaultProvider: ex.provider,
				defaultModel:    ex.model,
				failover:        ex.failover,
			},
			&CallLLMStage{
				failover:     ex.failover,
				tools:        ex.tools,
				notifySwitch: ex.notifySwitch,
			},
			&RecordUsageStage{usage: ex.usage},
			&CheckCompletionStage{},
			&AnnouncePlanStage{
				planner:   ex.planner,
				publisher: ex.publisher,
				workspace: ex.workspace,
			},
			&ExecuteToolsStage{
				tools:     ex.tools,
				sessions:  ex.sessions,
				publisher: ex.publisher,
			},
		},
	}
}
