package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// ActionStatus represents the status of an action
type ActionStatus string

const (
	ActionRunning   ActionStatus = "running"
	ActionSuccess   ActionStatus = "success"
	ActionError     ActionStatus = "error"
	ActionSkipped   ActionStatus = "skipped"
)

// ActionType categorizes actions for smart filtering
type ActionType string

const (
	ActionTypeExec      ActionType = "exec"      // Command execution
	ActionTypeWeb       ActionType = "web"       // Web searches
	ActionTypeFile      ActionType = "file"      // File operations
	ActionTypeMessage   ActionType = "message"   // Messaging
	ActionTypeSubagent  ActionType = "subagent"  // Subagent spawns
	ActionTypeInternal  ActionType = "internal"  // Internal operations
)

// Action represents a single tracked action (tool execution)
type Action struct {
	ID          string
	ToolName    string
	Type        ActionType
	Status      ActionStatus
	StartTime   time.Time
	EndTime     time.Time
	Duration    time.Duration
	Result      string       // Truncated result
	FullResult  string       // Full result (not sent to Telegram)
	Error       string
}

// ActionStream tracks and formats action updates for visibility
type ActionStream struct {
	actions         []Action
	config          config.VisibilityConfig
	lastUpdateTime  time.Time
	updateCallback  func(summary string) // Callback to send updates
	mu              sync.RWMutex
}

// NewActionStream creates a new action stream
func NewActionStream(cfg config.VisibilityConfig, updateCallback func(summary string)) *ActionStream {
	return &ActionStream{
		actions:        make([]Action, 0),
		config:         cfg,
		lastUpdateTime: time.Now(),
		updateCallback: updateCallback,
	}
}

// StartAction records the start of an action
func (as *ActionStream) StartAction(toolName string, args map[string]interface{}) string {
	as.mu.Lock()
	defer as.mu.Unlock()

	// Determine action type and whether to show it
	actionType := classifyAction(toolName)

	// Skip internal actions in non-verbose mode
	if !as.config.VerboseMode && actionType == ActionTypeInternal {
		return "" // Return empty ID to signal "skip tracking"
	}

	actionID := fmt.Sprintf("%s-%d", toolName, time.Now().UnixNano())
	action := Action{
		ID:        actionID,
		ToolName:  toolName,
		Type:      actionType,
		Status:    ActionRunning,
		StartTime: time.Now(),
	}

	as.actions = append(as.actions, action)

	// Trigger update if enough time has passed
	as.maybeUpdate()

	return actionID
}

// CompleteAction marks an action as complete
func (as *ActionStream) CompleteAction(actionID string, result string, err error) {
	if actionID == "" {
		return // Skipped action
	}

	as.mu.Lock()
	defer as.mu.Unlock()

	for i := range as.actions {
		if as.actions[i].ID == actionID {
			as.actions[i].EndTime = time.Now()
			as.actions[i].Duration = as.actions[i].EndTime.Sub(as.actions[i].StartTime)

			if err != nil {
				as.actions[i].Status = ActionError
				as.actions[i].Error = err.Error()
			} else {
				as.actions[i].Status = ActionSuccess
				as.actions[i].FullResult = result
				as.actions[i].Result = as.truncateResult(result, as.actions[i].Type)
			}

			// Trigger update
			as.maybeUpdate()
			break
		}
	}
}

// maybeUpdate triggers an update if enough time has passed
func (as *ActionStream) maybeUpdate() {
	now := time.Now()
	if now.Sub(as.lastUpdateTime) >= time.Duration(as.config.UpdateIntervalMS)*time.Millisecond {
		as.lastUpdateTime = now
		if as.updateCallback != nil {
			summary := as.formatSummary()
			as.updateCallback(summary)
		}
	}
}

// ForceUpdate immediately sends an update
func (as *ActionStream) ForceUpdate() {
	as.mu.RLock()
	defer as.mu.RUnlock()

	if as.updateCallback != nil {
		summary := as.formatSummary()
		as.updateCallback(summary)
	}
}

// formatSummary creates a compact summary of all actions
func (as *ActionStream) formatSummary() string {
	if len(as.actions) == 0 {
		return "Thinking... 💭"
	}

	var sb strings.Builder

	// Count running actions
	running := 0
	for _, a := range as.actions {
		if a.Status == ActionRunning {
			running++
		}
	}

	if running > 0 {
		sb.WriteString("Working... 🔧\n")
	} else {
		sb.WriteString("Finishing up... 🔧\n")
	}

	for _, action := range as.actions {
		icon := as.getStatusIcon(action.Status)
		sb.WriteString(fmt.Sprintf("%s %s", icon, as.formatActionName(action)))

		// Add duration if enabled and action is complete
		if as.config.ShowDuration && action.Status != ActionRunning && action.Duration > 0 {
			sb.WriteString(fmt.Sprintf(" (%dms)", action.Duration.Milliseconds()))
		}

		sb.WriteString("\n")

		// Only show errors, not full results (keeps Telegram messages compact)
		if action.Error != "" {
			sb.WriteString(fmt.Sprintf("  ✗ %s\n", utils.Truncate(action.Error, 100)))
		}
	}

	return sb.String()
}

// getStatusIcon returns an icon for the action status
func (as *ActionStream) getStatusIcon(status ActionStatus) string {
	switch status {
	case ActionRunning:
		return "⏳"
	case ActionSuccess:
		return "✓"
	case ActionError:
		return "✗"
	case ActionSkipped:
		return "○"
	default:
		return "•"
	}
}

// formatActionName creates a readable name for an action
func (as *ActionStream) formatActionName(action Action) string {
	switch action.ToolName {
	case "exec":
		return "Running command"
	case "web_search":
		return "Searching web"
	case "read_file":
		return "Reading file"
	case "write_file":
		return "Writing file"
	case "list_files":
		return "Listing files"
	case "spawn":
		return "Spawning subagent"
	case "message":
		return "Sending message"
	default:
		return fmt.Sprintf("Executing %s", action.ToolName)
	}
}

// truncateResult truncates a result based on action type
func (as *ActionStream) truncateResult(result string, actionType ActionType) string {
	var maxLen int

	switch actionType {
	case ActionTypeExec:
		maxLen = 800 // Commands can be longer
	case ActionTypeFile:
		maxLen = 500 // Files: show head+tail
	case ActionTypeWeb:
		maxLen = 600 // Web results
	default:
		maxLen = 400 // Default
	}

	if len(result) <= maxLen {
		return result
	}

	// For file operations, show head + tail
	if actionType == ActionTypeFile {
		headLen := maxLen / 2
		tailLen := maxLen - headLen - 20 // Leave room for separator
		head := result[:headLen]
		tail := result[len(result)-tailLen:]
		return fmt.Sprintf("%s\n... [%d chars omitted] ...\n%s",
			head, len(result)-headLen-tailLen, tail)
	}

	// For others, simple truncation
	return utils.Truncate(result, maxLen)
}

// classifyAction determines the action type based on tool name
func classifyAction(toolName string) ActionType {
	switch toolName {
	case "exec":
		return ActionTypeExec
	case "web_search", "brave_search", "duckduckgo_search":
		return ActionTypeWeb
	case "read_file", "write_file", "list_files", "delete_file":
		return ActionTypeFile
	case "message":
		return ActionTypeMessage
	case "spawn":
		return ActionTypeSubagent
	default:
		// Quick file reads are considered internal
		if strings.HasPrefix(toolName, "read_") {
			return ActionTypeInternal
		}
		return ActionTypeInternal
	}
}

// GetActionCount returns the number of tracked actions
func (as *ActionStream) GetActionCount() int {
	as.mu.RLock()
	defer as.mu.RUnlock()
	return len(as.actions)
}

// Clear clears all tracked actions
func (as *ActionStream) Clear() {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.actions = make([]Action, 0)
}
