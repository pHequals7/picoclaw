// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/attachments"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/failover"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/state"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/usage"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type AgentLoop struct {
	bus            *bus.MessageBus
	provider       providers.LLMProvider
	workspace      string
	model          string
	contextWindow  int // Maximum context window size in tokens
	maxIterations  int
	sessions       *session.SessionManager
	state          *state.Manager
	failoverMgr    *failover.Manager
	contextBuilder *ContextBuilder
	tools          *tools.ToolRegistry
	usageStore     *usage.Store
	config         *config.Config
	running        atomic.Bool
	summarizing    sync.Map // Tracks which sessions are currently being summarized
	activeCancel   sync.Map // sessionKey -> context.CancelFunc for in-flight requests
	probeRunning   atomic.Bool
	noticeMu       sync.Mutex
	lastNoticeByEP int64
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string        // Session identifier for history/context
	Channel         string        // Target channel for tool execution
	ChatID          string        // Target chat ID for tool execution
	UserMessage     string        // User message content (may include prefix)
	DefaultResponse string        // Response when LLM returns empty
	EnableSummary   bool          // Whether to trigger summarization
	SendResponse    bool          // Whether to send response via bus
	NoHistory       bool          // If true, don't load session history (for heartbeat)
	CorrelationID   string        // Correlation ID for request tracing
	ActionStream    *ActionStream // Action stream for visibility (optional)
	Media           []string      // Media file paths (images, etc.)
}

// createToolRegistry creates a tool registry with common tools.
// This is shared between main agent and subagents.
func createToolRegistry(workspace string, restrict bool, cfg *config.Config, msgBus *bus.MessageBus) *tools.ToolRegistry {
	registry := tools.NewToolRegistry()
	attachmentStore := attachments.NewStore(workspace)

	// File system tools
	registry.Register(tools.NewReadFileTool(workspace, restrict))
	registry.Register(tools.NewWriteFileTool(workspace, restrict))
	registry.Register(tools.NewListDirTool(workspace, restrict))
	registry.Register(tools.NewEditFileTool(workspace, restrict))
	registry.Register(tools.NewAppendFileTool(workspace, restrict))
	registry.Register(tools.NewImportAttachmentTool(workspace, restrict, attachmentStore))

	// Shell execution
	registry.Register(tools.NewExecTool(workspace, restrict))

	if searchTool := tools.NewWebSearchTool(tools.WebSearchToolOptions{
		BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
		BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
		DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
	}); searchTool != nil {
		registry.Register(searchTool)
	}
	registry.Register(tools.NewWebFetchTool(50000))

	// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
	registry.Register(tools.NewI2CTool())
	registry.Register(tools.NewSPITool())

	// Message tool - available to both agent and subagent
	// Subagent uses it to communicate directly with user
	messageTool := tools.NewMessageTool()
	messageTool.SetSendCallback(func(channel, chatID, content string) error {
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
		})
		return nil
	})
	registry.Register(messageTool)

	// Send file tool - allows agent to send files to user
	sendFileTool := tools.NewSendFileTool(workspace)
	sendFileTool.SetSendCallback(func(channel, chatID, caption string, files []string) error {
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: caption,
			Media:   files,
		})
		return nil
	})
	registry.Register(sendFileTool)

	return registry
}

func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus, provider providers.LLMProvider) *AgentLoop {
	workspace := cfg.WorkspacePath()
	os.MkdirAll(workspace, 0755)
	os.MkdirAll(filepath.Join(workspace, "downloads"), 0755)
	mediaCacheDir := filepath.Join(workspace, "tmp", "media")
	if err := utils.SetMediaCacheDir(mediaCacheDir); err != nil {
		logger.WarnCF("agent", "Failed to configure workspace media cache directory",
			map[string]interface{}{"dir": mediaCacheDir, "error": err.Error()})
	} else {
		logger.InfoCF("agent", "Configured media cache directory",
			map[string]interface{}{"dir": mediaCacheDir})
	}

	restrict := cfg.Agents.Defaults.RestrictToWorkspace

	// Create tool registry for main agent
	toolsRegistry := createToolRegistry(workspace, restrict, cfg, msgBus)

	// Register MCP-discovered tools (best effort; continue on per-server failures)
	mcpTools, mcpErr := tools.LoadMCPTools(context.Background(), cfg.Tools.MCP, workspace)
	if mcpErr != nil {
		logger.WarnCF("agent", "Some MCP servers failed to load",
			map[string]interface{}{
				"error": mcpErr.Error(),
			})
	}
	for _, tool := range mcpTools {
		toolsRegistry.Register(tool)
	}

	// Create subagent manager with its own tool registry
	subagentManager := tools.NewSubagentManager(provider, cfg.Agents.Defaults.Model, workspace, msgBus)
	subagentTools := createToolRegistry(workspace, restrict, cfg, msgBus)
	// Subagent doesn't need spawn/subagent tools to avoid recursion
	subagentManager.SetTools(subagentTools)

	// Register spawn tool (for main agent)
	spawnTool := tools.NewSpawnTool(subagentManager)
	toolsRegistry.Register(spawnTool)

	// Register subagent tool (synchronous execution)
	subagentTool := tools.NewSubagentTool(subagentManager)
	toolsRegistry.Register(subagentTool)

	sessionsManager := session.NewSessionManager(filepath.Join(workspace, "sessions"))

	// Create state manager for atomic state persistence
	stateManager := state.NewManager(workspace)
	failoverManager := failover.NewManager(cfg, stateManager)
	// Reuse the primary provider instance for the primary model route.
	failoverManager.SetProviderForModel(cfg.Agents.Defaults.Model, provider)

	// Create context builder and set tools registry
	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)

	return &AgentLoop{
		bus:            msgBus,
		provider:       provider,
		workspace:      workspace,
		model:          cfg.Agents.Defaults.Model,
		contextWindow:  cfg.Agents.Defaults.MaxTokens, // Restore context window for summarization
		maxIterations:  cfg.Agents.Defaults.MaxToolIterations,
		sessions:       sessionsManager,
		state:          stateManager,
		failoverMgr:    failoverManager,
		contextBuilder: contextBuilder,
		tools:          toolsRegistry,
		usageStore:     usage.NewStore(filepath.Join(workspace, "usage")),
		config:         cfg,
		summarizing:    sync.Map{},
	}
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)

	for al.running.Load() {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			// Handle /stop command: cancel the active request for this session
			if strings.TrimSpace(msg.Content) == "/stop" {
				sessionKey := fmt.Sprintf("%s:%s", msg.Channel, msg.ChatID)
				if cancelFn, ok := al.activeCancel.LoadAndDelete(sessionKey); ok {
					cancelFn.(context.CancelFunc)()
					logger.InfoCF("agent", "Cancelled active request", map[string]interface{}{
						"session_key": sessionKey,
					})
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: "Stopped.",
					})
				} else {
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: "Nothing running to stop.",
					})
				}
				continue
			}

			// Create a cancellable context for this request
			msgCtx, msgCancel := context.WithCancel(ctx)
			sessionKey := fmt.Sprintf("%s:%s", msg.Channel, msg.ChatID)
			al.activeCancel.Store(sessionKey, msgCancel)

			response, err := al.processMessage(msgCtx, msg)
			al.activeCancel.Delete(sessionKey)
			msgCancel() // clean up context

			if err != nil {
				if msgCtx.Err() == context.Canceled {
					// Request was cancelled by /stop, don't send error
					continue
				}
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			if response != "" {
				// Check if the message tool already sent a response during this round.
				// If so, skip publishing to avoid duplicate messages to the user.
				alreadySent := false
				if tool, ok := al.tools.Get("message"); ok {
					if mt, ok := tool.(*tools.MessageTool); ok {
						alreadySent = mt.HasSentInRound()
					}
				}

				if !alreadySent {
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: response,
					})
				}
			}
		}
	}

	return nil
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.tools.Register(tool)
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	return al.state.SetLastChatID(chatID)
}

func (al *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return al.processMessage(ctx, msg)
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
func (al *AgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      "heartbeat",
		Channel:         channel,
		ChatID:          chatID,
		UserMessage:     content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       true, // Don't load session history for heartbeat
	})
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]interface{}{
			"channel":        msg.Channel,
			"chat_id":        msg.ChatID,
			"sender_id":      msg.SenderID,
			"session_key":    msg.SessionKey,
			"correlation_id": msg.CorrelationID,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	trimmed := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(trimmed, "/usage") {
		return al.handleUsageCommand(msg, trimmed), nil
	}
	if al.failoverMgr != nil && al.failoverMgr.Enabled() {
		if decision := al.failoverMgr.HandleUserSwitchbackDecision(trimmed); decision.Handled {
			if decision.Reply != "" {
				return decision.Reply, nil
			}
			return "Acknowledged.", nil
		}
		al.maybeRunFailoverProbe(msg.Channel, msg.ChatID)
	}

	// Create ActionStream for visibility if enabled
	var actionStream *ActionStream
	if al.config.Visibility.Enabled {
		// Create callback to send updates via message bus
		updateCallback := func(summary string) {
			al.bus.PublishOutbound(bus.OutboundMessage{
				Channel:          msg.Channel,
				ChatID:           msg.ChatID,
				Content:          summary,
				IsProgressUpdate: true,
			})
		}
		actionStream = NewActionStream(al.config.Visibility, updateCallback)
	}

	// Process as user message
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      msg.SessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
		CorrelationID:   msg.CorrelationID,
		ActionStream:    actionStream,
		Media:           msg.Media,
	})
}

func formatUsageAggregatePlain(label string, agg usage.Aggregate) string {
	return fmt.Sprintf(
		"%s: calls=%d known=%d unknown=%d in=%s (%s) out=%s (%s) total=%s (%s)",
		label,
		agg.Calls,
		agg.KnownCalls,
		agg.UnknownCalls,
		usage.GroupedInt(agg.PromptTokens),
		usage.HumanTokens(agg.PromptTokens),
		usage.GroupedInt(agg.CompletionTokens),
		usage.HumanTokens(agg.CompletionTokens),
		usage.GroupedInt(agg.TotalTokens),
		usage.HumanTokens(agg.TotalTokens),
	)
}

func formatUsageAggregateTable(label string, agg usage.Aggregate) string {
	return fmt.Sprintf("| %-14s | %5d | %7s | %6s | %7s |",
		label,
		agg.Calls,
		usage.HumanTokens(agg.PromptTokens),
		usage.HumanTokens(agg.CompletionTokens),
		usage.HumanTokens(agg.TotalTokens),
	)
}

func usageTableHeader() string {
	return "| Scope          | Calls |   Input | Output |   Total |\n" +
		"|----------------|-------|---------|--------|---------|"
}

func (al *AgentLoop) handleUsageCommand(msg bus.InboundMessage, command string) string {
	parts := strings.Fields(command)
	mode := ""
	if len(parts) > 1 {
		mode = strings.ToLower(parts[1])
	}

	dayKey := al.usageStore.TodayKey()
	sessionKey := msg.SessionKey
	if sessionKey == "" {
		sessionKey = fmt.Sprintf("%s:%s", msg.Channel, msg.ChatID)
	}

	switch mode {
	case "last":
		last, ok := al.usageStore.LastBySession(sessionKey)
		if !ok {
			return "No usage records found for this session yet."
		}
		return fmt.Sprintf(
			"Last usage (%s, %s): known=%t in=%s (%s) out=%s (%s) total=%s (%s) provider=%s model=%s reason=%s",
			last.Timestamp.Format(time.RFC3339),
			last.DayKey,
			last.UsageKnown,
			usage.GroupedInt(last.PromptTokens),
			usage.HumanTokens(last.PromptTokens),
			usage.GroupedInt(last.CompletionTokens),
			usage.HumanTokens(last.CompletionTokens),
			usage.GroupedInt(last.TotalTokens),
			usage.HumanTokens(last.TotalTokens),
			last.Provider,
			last.Model,
			last.Reason,
		)
	case "session":
		records := al.usageStore.Query(usage.Filter{SessionKey: sessionKey, Limit: 20})
		if len(records) == 0 {
			return "No usage records found for this session yet."
		}
		lines := []string{
			fmt.Sprintf("Session usage (%s) latest %d:", sessionKey, len(records)),
			formatUsageAggregatePlain("Summary", usage.AggregateRecords(records)),
		}
		for _, r := range records {
			lines = append(lines, fmt.Sprintf(
				"- %s provider=%s model=%s known=%t in=%s (%s) out=%s (%s) total=%s (%s) reason=%s",
				r.Timestamp.Format(time.RFC3339),
				r.Provider,
				r.Model,
				r.UsageKnown,
				usage.GroupedInt(r.PromptTokens),
				usage.HumanTokens(r.PromptTokens),
				usage.GroupedInt(r.CompletionTokens),
				usage.HumanTokens(r.CompletionTokens),
				usage.GroupedInt(r.TotalTokens),
				usage.HumanTokens(r.TotalTokens),
				r.Reason,
			))
		}
		return strings.Join(lines, "\n")
	case "today":
		records := al.usageStore.Query(usage.Filter{DayKey: dayKey})
		if len(records) == 0 {
			return fmt.Sprintf("No usage records for today (%s) yet.", dayKey)
		}
		lines := []string{
			fmt.Sprintf("Today usage (%s):", dayKey),
			formatUsageAggregatePlain("Summary", usage.AggregateRecords(records)),
			"By provider:",
		}
		byProvider := usage.ProviderBreakdown(records)
		providers := make([]string, 0, len(byProvider))
		for p := range byProvider {
			providers = append(providers, p)
		}
		sort.Strings(providers)
		for _, p := range providers {
			lines = append(lines, "  "+formatUsageAggregatePlain(p, byProvider[p]))
		}
		return strings.Join(lines, "\n")
	case "provider":
		todayRecords := al.usageStore.Query(usage.Filter{DayKey: dayKey})
		sessionRecords := al.usageStore.Query(usage.Filter{SessionKey: sessionKey})
		if len(todayRecords) == 0 && len(sessionRecords) == 0 {
			return "No usage records found yet."
		}
		lines := []string{
			fmt.Sprintf("Provider usage (today %s + session %s):", dayKey, sessionKey),
			"Today by provider:",
		}
		todayByProvider := usage.ProviderBreakdown(todayRecords)
		sessionByProvider := usage.ProviderBreakdown(sessionRecords)
		todayKeys := make([]string, 0, len(todayByProvider))
		for p := range todayByProvider {
			todayKeys = append(todayKeys, p)
		}
		sort.Strings(todayKeys)
		if len(todayKeys) == 0 {
			lines = append(lines, "  none")
		}
		for _, p := range todayKeys {
			lines = append(lines, "  "+formatUsageAggregatePlain(p, todayByProvider[p]))
		}
		lines = append(lines, "Session by provider:")
		sessionKeys := make([]string, 0, len(sessionByProvider))
		for p := range sessionByProvider {
			sessionKeys = append(sessionKeys, p)
		}
		sort.Strings(sessionKeys)
		if len(sessionKeys) == 0 {
			lines = append(lines, "  none")
		}
		for _, p := range sessionKeys {
			lines = append(lines, "  "+formatUsageAggregatePlain(p, sessionByProvider[p]))
		}
		return strings.Join(lines, "\n")
	default:
		todayRecords := al.usageStore.Query(usage.Filter{DayKey: dayKey})
		sessionRecords := al.usageStore.Query(usage.Filter{SessionKey: sessionKey})
		last, hasLast := al.usageStore.LastBySession(sessionKey)
		// Header
		lastLine := "Last: none"
		if hasLast {
			lastLine = fmt.Sprintf("Last call: `%s` · %s · %s in / %s out",
				last.Model,
				last.Provider,
				usage.HumanTokens(last.PromptTokens),
				usage.HumanTokens(last.CompletionTokens),
			)
		}
		sessionAgg := usage.AggregateRecords(sessionRecords)
		todayAgg := usage.AggregateRecords(todayRecords)
		lines := []string{
			fmt.Sprintf("**Usage Dashboard** · %s · `%s`", dayKey, sessionKey),
			"",
			lastLine,
			"",
			usageTableHeader(),
			formatUsageAggregateTable("This session", sessionAgg),
			formatUsageAggregateTable("Today", todayAgg),
		}
		byProvider := usage.ProviderBreakdown(todayRecords)
		if len(byProvider) > 0 {
			keys := make([]string, 0, len(byProvider))
			for p := range byProvider {
				keys = append(keys, p)
			}
			sort.Strings(keys)
			for _, p := range keys {
				lines = append(lines, formatUsageAggregateTable("  └ "+p, byProvider[p]))
			}
		}
		lines = append(lines, "")
		lines = append(lines, "_/usage last · session · today · provider_")
		return strings.Join(lines, "\n")
	}
}

func (al *AgentLoop) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Verify this is a system message
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id":      msg.SenderID,
			"chat_id":        msg.ChatID,
			"correlation_id": msg.CorrelationID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
	} else {
		// Fallback
		originChannel = "cli"
	}

	// Extract subagent result from message content
	// Format: "Task 'label' completed.\n\nResult:\n<actual content>"
	content := msg.Content
	if idx := strings.Index(content, "Result:\n"); idx >= 0 {
		content = content[idx+8:] // Extract just the result part
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]interface{}{
				"sender_id":   msg.SenderID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// Agent acts as dispatcher only - subagent handles user interaction via message tool
	// Don't forward result here, subagent should use message tool to communicate with user
	logger.InfoCF("agent", "Subagent completed",
		map[string]interface{}{
			"sender_id":   msg.SenderID,
			"channel":     originChannel,
			"content_len": len(content),
		})

	// Agent only logs, does not respond to user
	return "", nil
}

// runAgentLoop is the core message processing logic.
// It handles context building, LLM calls, tool execution, and response handling.
func (al *AgentLoop) runAgentLoop(ctx context.Context, opts processOptions) (string, error) {
	defer al.cleanupTurnMedia(opts.Media)

	// 0. Record last channel for heartbeat notifications (skip internal channels)
	if opts.Channel != "" && opts.ChatID != "" {
		// Don't record internal channels (cli, system, subagent)
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := al.RecordLastChannel(channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel: %v", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	// 1. Update tool contexts
	al.updateToolContexts(opts.Channel, opts.ChatID)

	// 2. Build messages (skip history for heartbeat)
	var history []providers.Message
	var summary string
	if !opts.NoHistory {
		history = al.sessions.GetHistory(opts.SessionKey)
		summary = al.sessions.GetSummary(opts.SessionKey)
	}
	messages := al.contextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		opts.Media,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	// 4. Run LLM iteration loop
	finalContent, iteration, err := al.runLLMIteration(ctx, messages, opts)
	if err != nil {
		return "", err
	}

	// If last tool had ForUser content and we already sent it, we might not need to send final response
	// This is controlled by the tool's Silent flag and ForUser content

	// 5. Handle empty response
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	// 6. Save final assistant message to session
	al.sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
	al.sessions.Save(opts.SessionKey)

	// 7. Optional: summarization
	if opts.EnableSummary {
		al.maybeSummarize(opts.SessionKey)
	}

	// 8. Optional: send response via bus
	if opts.SendResponse {
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	// 9. Log response
	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]interface{}{
			"session_key":    opts.SessionKey,
			"iterations":     iteration,
			"final_length":   len(finalContent),
			"correlation_id": opts.CorrelationID,
		})

	return finalContent, nil
}

func (al *AgentLoop) cleanupTurnMedia(media []string) {
	if len(media) == 0 {
		return
	}

	workspaceMediaDir := filepath.Clean(filepath.Join(al.workspace, "tmp", "media"))
	legacyTempDir := filepath.Clean(filepath.Join(os.TempDir(), "picoclaw_media"))

	for _, p := range media {
		cleanPath := filepath.Clean(p)
		if !isPathWithin(cleanPath, workspaceMediaDir) && !isPathWithin(cleanPath, legacyTempDir) {
			continue
		}

		if err := os.Remove(cleanPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			logger.DebugCF("agent", "Failed to remove media file after turn",
				map[string]interface{}{"path": cleanPath, "error": err.Error()})
			continue
		}

		logger.DebugCF("agent", "Removed media file after turn",
			map[string]interface{}{"path": cleanPath})
	}
}

func isPathWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// runLLMIteration executes the LLM call loop with tool handling.
// Returns the final content, iteration count, and any error.
func (al *AgentLoop) runLLMIteration(ctx context.Context, messages []providers.Message, opts processOptions) (string, int, error) {
	iteration := 0
	var finalContent string
	planState := newExecutionPlanState()

	for iteration < al.maxIterations {
		iteration++

		logger.DebugCF("agent", "LLM iteration",
			map[string]interface{}{
				"iteration": iteration,
				"max":       al.maxIterations,
			})

		// Build tool definitions
		providerToolDefs := al.tools.ToProviderDefs()
		activeProvider := al.provider
		activeModel := al.model
		switchEpoch := int64(0)
		if al.failoverMgr != nil && al.failoverMgr.Enabled() {
			route, routeErr := al.failoverMgr.ResolveRoute()
			if routeErr != nil {
				return "", iteration, fmt.Errorf("resolve failover route: %w", routeErr)
			}
			activeProvider = route.Provider
			activeModel = route.Model
			switchEpoch = route.SwitchEpoch
		}

		// Log LLM request details
		logger.DebugCF("agent", "LLM request",
			map[string]interface{}{
				"iteration":         iteration,
				"model":             activeModel,
				"messages_count":    len(messages),
				"tools_count":       len(providerToolDefs),
				"max_tokens":        8192,
				"temperature":       0.7,
				"system_prompt_len": len(messages[0].Content),
			})

		// Log full messages (detailed)
		logger.DebugCF("agent", "Full LLM request",
			map[string]interface{}{
				"iteration":     iteration,
				"messages_json": formatMessagesForLog(messages),
				"tools_json":    formatToolsForLog(providerToolDefs),
			})

		// Call LLM using routed model/provider
		response, err := activeProvider.Chat(ctx, messages, providerToolDefs, activeModel, map[string]interface{}{
			"max_tokens":  8192,
			"temperature": 0.7,
		})

		if err != nil {
			var rateLimitErr *providers.RateLimitError
			if al.failoverMgr != nil && al.failoverMgr.Enabled() && errors.As(err, &rateLimitErr) {
				switchEvent := al.failoverMgr.OnLLMRateLimited(activeModel, err)
				logger.WarnCF("agent", "Failover switch evaluation",
					map[string]interface{}{
						"iteration":      iteration,
						"from_model":     switchEvent.FromModel,
						"to_model":       switchEvent.ToModel,
						"reason":         switchEvent.Reason,
						"switched":       switchEvent.Switched,
						"status_code":    rateLimitErr.StatusCode,
						"switch_epoch":   switchEpoch,
						"correlation_id": opts.CorrelationID,
					})

				if switchEvent.Switched {
					al.notifyFailoverSwitch(opts.Channel, opts.ChatID, switchEvent)
					retryRoute, routeErr := al.failoverMgr.ResolveRoute()
					if routeErr != nil {
						return "", iteration, fmt.Errorf("resolve failover retry route: %w", routeErr)
					}
					activeProvider = retryRoute.Provider
					activeModel = retryRoute.Model
					switchEpoch = retryRoute.SwitchEpoch

					response, err = activeProvider.Chat(ctx, messages, providerToolDefs, activeModel, map[string]interface{}{
						"max_tokens":  8192,
						"temperature": 0.7,
					})
				}
			}

			if err != nil {
				logger.ErrorCF("agent", "LLM call failed",
					map[string]interface{}{
						"iteration":      iteration,
						"error":          err.Error(),
						"model":          activeModel,
						"switch_epoch":   switchEpoch,
						"correlation_id": opts.CorrelationID,
					})
				return "", iteration, fmt.Errorf("LLM call failed: %w", err)
			}
		}
		if al.failoverMgr != nil && al.failoverMgr.Enabled() {
			al.failoverMgr.OnLLMSuccess(activeModel)
		}

		if al.usageStore != nil {
			usageKnown := response.Usage != nil
			promptTokens := 0
			completionTokens := 0
			totalTokens := 0
			if usageKnown {
				promptTokens = response.Usage.PromptTokens
				completionTokens = response.Usage.CompletionTokens
				totalTokens = response.Usage.TotalTokens
			}
			if totalTokens == 0 {
				totalTokens = promptTokens + completionTokens
			}
			reason := strings.TrimSpace(response.FinishReason)
			if reason == "" {
				reason = "normal_call"
			}
			al.usageStore.Add(usage.Record{
				Timestamp:        time.Now().UTC(),
				SessionKey:       opts.SessionKey,
				DayKey:           time.Now().UTC().Format("2006-01-02"),
				Provider:         providerFromModel(activeModel),
				Model:            activeModel,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      totalTokens,
				UsageKnown:       usageKnown,
				Reason:           reason,
			})
		}

		// Check if no tool calls - we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
				map[string]interface{}{
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
			break
		}

		// Log tool calls
		toolNames := make([]string, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("agent", "LLM requested tool calls",
			map[string]interface{}{
				"tools":          toolNames,
				"count":          len(response.ToolCalls),
				"iteration":      iteration,
				"correlation_id": opts.CorrelationID,
			})

		// Plan+execute mode: first tool-call batch becomes explicit user-visible plan.
		// Persist the plan as a workspace artifact and publish it to chat.
		if !planState.Announced {
			planState.Bullets = buildExecutionPlanBullets(response.ToolCalls)
			planState.absorbToolCalls(response.ToolCalls)
			planState.Announced = true

			planPath, planErr := writeExecutionPlanFile(al.workspace, planState.Bullets, planFileMetadata{
				SessionKey:    opts.SessionKey,
				CorrelationID: opts.CorrelationID,
				Model:         activeModel,
			}, time.Now())
			if planErr != nil {
				logger.WarnCF("agent", "Failed to persist execution plan file",
					map[string]interface{}{
						"error":          planErr.Error(),
						"session_key":    opts.SessionKey,
						"correlation_id": opts.CorrelationID,
					})
			} else {
				logger.InfoCF("agent", "Execution plan file created",
					map[string]interface{}{
						"path":           planPath,
						"bullets":        len(planState.Bullets),
						"session_key":    opts.SessionKey,
						"correlation_id": opts.CorrelationID,
					})
			}

			planMsg := formatExecutionPlanProgressWithArtifact(planState.Bullets, planPath)
			if opts.Channel != "" && opts.ChatID != "" {
				// Send the plan as a regular message so it remains persistent in chat.
				// Telegram channel logic will finalize the current placeholder for this message.
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:          opts.Channel,
					ChatID:           opts.ChatID,
					Content:          planMsg,
					IsProgressUpdate: false,
				})
				// Immediately start a second message dedicated to streaming progress updates.
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:          opts.Channel,
					ChatID:           opts.ChatID,
					Content:          "Working... 🔧",
					IsProgressUpdate: true,
				})
			}
		}

		// Build assistant message with tool calls
		assistantMsg := providers.Message{
			Role:    "assistant",
			Content: response.Content,
		}
		for _, tc := range response.ToolCalls {
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
		messages = append(messages, assistantMsg)

		// Save assistant message with tool calls to session
		al.sessions.AddFullMessage(opts.SessionKey, assistantMsg)

		// Execute tool calls
		for _, tc := range response.ToolCalls {
			// If model introduces out-of-plan tool families, announce and persist plan update first.
			tcName := strings.TrimSpace(tc.Name)
			if tcName == "" && tc.Function != nil {
				tcName = strings.TrimSpace(tc.Function.Name)
			}
			if planState.Announced && tcName != "" && !planState.isAllowedTool(tcName) {
				updateStep := summarizeToolCallForPlan(tc)
				if len(planState.Bullets) < maxPlanBullets {
					planState.Bullets = append(planState.Bullets, updateStep)
				}
				planState.Allowed[tcName] = struct{}{}

				updateMsg := formatPlanUpdateProgress(updateStep)
				if opts.Channel != "" && opts.ChatID != "" {
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel:          opts.Channel,
						ChatID:           opts.ChatID,
						Content:          updateMsg,
						IsProgressUpdate: true,
					})
				}

			}

			// Log tool call with arguments preview
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"tool":           tc.Name,
					"iteration":      iteration,
					"correlation_id": opts.CorrelationID,
				})

			// Track action start if visibility enabled
			var actionID string
			if opts.ActionStream != nil {
				actionID = opts.ActionStream.StartAction(tc.Name, tc.Arguments)
			}

			// Create async callback for tools that implement AsyncTool
			// NOTE: Following openclaw's design, async tools do NOT send results directly to users.
			// Instead, they notify the agent via PublishInbound, and the agent decides
			// whether to forward the result to the user (in processSystemMessage).
			asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
				// Log the async completion but don't send directly to user
				// The agent will handle user notification via processSystemMessage
				if !result.Silent && result.ForUser != "" {
					logger.InfoCF("agent", "Async tool completed, agent will handle notification",
						map[string]interface{}{
							"tool":        tc.Name,
							"content_len": len(result.ForUser),
						})
				}
			}

			toolResult := al.tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, opts.Channel, opts.ChatID, asyncCallback)

			// Track action completion if visibility enabled
			if opts.ActionStream != nil && actionID != "" {
				resultContent := toolResult.ForUser
				if resultContent == "" {
					resultContent = toolResult.ForLLM
				}
				opts.ActionStream.CompleteAction(actionID, resultContent, toolResult.Err)
			}

			// Send ForUser content to user immediately if not Silent
			if !toolResult.Silent && toolResult.ForUser != "" && opts.SendResponse {
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: toolResult.ForUser,
				})
				logger.DebugCF("agent", "Sent tool result to user",
					map[string]interface{}{
						"tool":        tc.Name,
						"content_len": len(toolResult.ForUser),
					})
			}

			// Determine content for LLM based on tool result
			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			// Save tool result message to session
			al.sessions.AddFullMessage(opts.SessionKey, toolResultMsg)
		}
	}

	// Force final update if visibility enabled
	if opts.ActionStream != nil {
		opts.ActionStream.ForceUpdate()
	}

	return finalContent, iteration, nil
}

func (al *AgentLoop) maybeRunFailoverProbe(channel, chatID string) {
	if al.failoverMgr == nil || !al.failoverMgr.Enabled() {
		return
	}
	if !al.failoverMgr.ShouldProbe(time.Now()) {
		return
	}
	if !al.probeRunning.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer al.probeRunning.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		outcome := al.failoverMgr.RunProbe(ctx)
		logger.InfoCF("agent", "Failover probe completed",
			map[string]interface{}{
				"success":        outcome.Success,
				"became_healthy": outcome.BecameHealthy,
				"next_probe_at":  outcome.NextProbeAt.UTC().Format(time.RFC3339),
			})

		if channel == "" || chatID == "" {
			return
		}
		prompt := strings.TrimSpace(outcome.PromptText)
		if prompt == "" {
			if p, ok := al.failoverMgr.ShouldSendSwitchbackPrompt(time.Now()); ok {
				prompt = p
			}
		}
		if prompt != "" {
			al.bus.PublishOutbound(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: prompt,
			})
		}
	}()
}

func (al *AgentLoop) notifyFailoverSwitch(channel, chatID string, event failover.SwitchEvent) {
	if channel == "" || chatID == "" || !al.config.Agents.Failover.NotifyOnSwitch {
		return
	}

	epoch := int64(0)
	if al.failoverMgr != nil {
		epoch = al.failoverMgr.Snapshot().SwitchEpoch
	}

	al.noticeMu.Lock()
	if epoch > 0 && epoch <= al.lastNoticeByEP {
		al.noticeMu.Unlock()
		return
	}
	if epoch > 0 {
		al.lastNoticeByEP = epoch
	}
	al.noticeMu.Unlock()

	al.bus.PublishOutbound(bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: fmt.Sprintf("Failover active: switched from %s to %s due to provider rate limits.", event.FromModel, event.ToModel),
	})
}

func providerFromModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "claude"), strings.Contains(m, "anthropic"):
		return "anthropic"
	case strings.Contains(m, "gpt"), strings.Contains(m, "openai"):
		return "openai"
	case strings.Contains(m, "gemini"), strings.Contains(m, "google"):
		return "google"
	case strings.Contains(m, "deepseek"):
		return "deepseek"
	default:
		return "unknown"
	}
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (al *AgentLoop) updateToolContexts(channel, chatID string) {
	// Use ContextualTool interface instead of type assertions
	if tool, ok := al.tools.Get("message"); ok {
		if mt, ok := tool.(tools.ContextualTool); ok {
			mt.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("spawn"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("subagent"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("send_file"); ok {
		if sf, ok := tool.(tools.ContextualTool); ok {
			sf.SetContext(channel, chatID)
		}
	}
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
func (al *AgentLoop) maybeSummarize(sessionKey string) {
	newHistory := al.sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)
	threshold := al.contextWindow * 75 / 100

	if len(newHistory) > 20 || tokenEstimate > threshold {
		if _, loading := al.summarizing.LoadOrStore(sessionKey, true); !loading {
			go func() {
				defer al.summarizing.Delete(sessionKey)
				al.summarizeSession(sessionKey)
			}()
		}
	}
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	info := make(map[string]interface{})

	// Tools info
	tools := al.tools.List()
	info["tools"] = map[string]interface{}{
		"count": len(tools),
		"names": tools,
	}

	// Skills info
	info["skills"] = al.contextBuilder.GetSkillsInfo()

	return info
}

// formatMessagesForLog formats messages for logging
func formatMessagesForLog(messages []providers.Message) string {
	if len(messages) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, msg := range messages {
		result += fmt.Sprintf("  [%d] Role: %s\n", i, msg.Role)
		if msg.ToolCalls != nil && len(msg.ToolCalls) > 0 {
			result += "  ToolCalls:\n"
			for _, tc := range msg.ToolCalls {
				result += fmt.Sprintf("    - ID: %s, Type: %s, Name: %s\n", tc.ID, tc.Type, tc.Name)
				if tc.Function != nil {
					result += fmt.Sprintf("      Arguments: %s\n", utils.Truncate(tc.Function.Arguments, 200))
				}
			}
		}
		if msg.Content != "" {
			content := utils.Truncate(msg.Content, 200)
			result += fmt.Sprintf("  Content: %s\n", content)
		}
		if msg.ToolCallID != "" {
			result += fmt.Sprintf("  ToolCallID: %s\n", msg.ToolCallID)
		}
		result += "\n"
	}
	result += "]"
	return result
}

// formatToolsForLog formats tool definitions for logging
func formatToolsForLog(tools []providers.ToolDefinition) string {
	if len(tools) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, tool := range tools {
		result += fmt.Sprintf("  [%d] Type: %s, Name: %s\n", i, tool.Type, tool.Function.Name)
		result += fmt.Sprintf("      Description: %s\n", tool.Function.Description)
		if len(tool.Function.Parameters) > 0 {
			result += fmt.Sprintf("      Parameters: %s\n", utils.Truncate(fmt.Sprintf("%v", tool.Function.Parameters), 200))
		}
	}
	result += "]"
	return result
}

// summarizeSession summarizes the conversation history for a session.
func (al *AgentLoop) summarizeSession(sessionKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := al.sessions.GetHistory(sessionKey)
	summary := al.sessions.GetSummary(sessionKey)

	// Keep last 4 messages for continuity
	if len(history) <= 4 {
		return
	}

	toSummarize := history[:len(history)-4]

	// Oversized Message Guard
	// Skip messages larger than 50% of context window to prevent summarizer overflow
	maxMessageTokens := al.contextWindow / 2
	validMessages := make([]providers.Message, 0)
	omitted := false

	for _, m := range toSummarize {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		// Estimate tokens for this message
		msgTokens := len(m.Content) / 4
		if msgTokens > maxMessageTokens {
			omitted = true
			continue
		}
		validMessages = append(validMessages, m)
	}

	if len(validMessages) == 0 {
		return
	}

	// Multi-Part Summarization
	// Split into two parts if history is significant
	var finalSummary string
	if len(validMessages) > 10 {
		mid := len(validMessages) / 2
		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := al.summarizeBatch(ctx, part1, "")
		s2, _ := al.summarizeBatch(ctx, part2, "")

		// Merge them
		mergePrompt := fmt.Sprintf("Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s", s1, s2)
		resp, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: mergePrompt}}, nil, al.model, map[string]interface{}{
			"max_tokens":  1024,
			"temperature": 0.3,
		})
		if err == nil {
			finalSummary = resp.Content
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = al.summarizeBatch(ctx, validMessages, summary)
	}

	if omitted && finalSummary != "" {
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	if finalSummary != "" {
		al.sessions.SetSummary(sessionKey, finalSummary)
		al.sessions.TruncateHistory(sessionKey, 4)
		al.sessions.Save(sessionKey)
	}
}

// summarizeBatch summarizes a batch of messages.
func (al *AgentLoop) summarizeBatch(ctx context.Context, batch []providers.Message, existingSummary string) (string, error) {
	prompt := "Provide a concise summary of this conversation segment, preserving core context and key points.\n"
	if existingSummary != "" {
		prompt += "Existing context: " + existingSummary + "\n"
	}
	prompt += "\nCONVERSATION:\n"
	for _, m := range batch {
		prompt += fmt.Sprintf("%s: %s\n", m.Role, m.Content)
	}

	response, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, al.model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	})
	if err != nil {
		return "", err
	}
	return response.Content, nil
}

// estimateTokens estimates the number of tokens in a message list.
// Uses rune count instead of byte length so that CJK and other multi-byte
// characters are not over-counted (a Chinese character is 3 bytes but roughly
// one token).
func (al *AgentLoop) estimateTokens(messages []providers.Message) int {
	total := 0
	for _, m := range messages {
		total += utf8.RuneCountInString(m.Content) / 3
	}
	return total
}
