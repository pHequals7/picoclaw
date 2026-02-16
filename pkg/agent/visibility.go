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
	Args        map[string]interface{} // Tool arguments for descriptive summaries
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
		Args:      args,
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

	// Separate completed, running, and errored actions
	var completed []Action
	var running []Action
	var errored []Action
	for _, a := range as.actions {
		switch a.Status {
		case ActionRunning:
			running = append(running, a)
		case ActionError:
			errored = append(errored, a)
		default:
			completed = append(completed, a)
		}
	}

	// Show compact completed count (not each individual line)
	if len(completed) > 0 {
		sb.WriteString(fmt.Sprintf("✓ %d step%s done\n", len(completed), pluralS(len(completed))))
	}

	// Show errors briefly
	for _, a := range errored {
		sb.WriteString(fmt.Sprintf("✗ %s: %s\n", as.formatActionName(a), utils.Truncate(a.Error, 60)))
	}

	// Show currently running action(s) with description
	for _, a := range running {
		sb.WriteString(fmt.Sprintf("⏳ %s\n", as.formatActionName(a)))
	}

	// If nothing is running, we're finishing up
	if len(running) == 0 && len(errored) == 0 {
		sb.WriteString("Finishing up... 🔧\n")
	}

	return sb.String()
}

// pluralS returns "s" if n != 1
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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

// formatActionName creates a brief, descriptive name for an action (5-7 words)
func (as *ActionStream) formatActionName(action Action) string {
	switch action.ToolName {
	case "exec":
		return summarizeCommand(action.Args)
	case "web_search":
		if q, ok := action.Args["query"].(string); ok {
			return fmt.Sprintf("Searching: %s", utils.Truncate(q, 30))
		}
		return "Searching the web"
	case "read_file":
		if p, ok := action.Args["path"].(string); ok {
			return fmt.Sprintf("Reading %s", shortenPath(p))
		}
		return "Reading a file"
	case "write_file":
		if p, ok := action.Args["path"].(string); ok {
			return fmt.Sprintf("Writing %s", shortenPath(p))
		}
		return "Writing a file"
	case "list_files":
		if p, ok := action.Args["path"].(string); ok {
			return fmt.Sprintf("Listing %s", shortenPath(p))
		}
		return "Listing files"
	case "spawn":
		if task, ok := action.Args["task"].(string); ok {
			return fmt.Sprintf("Subagent: %s", utils.Truncate(task, 30))
		}
		return "Running subagent"
	case "message":
		return "Sending message"
	default:
		return fmt.Sprintf("Running %s", action.ToolName)
	}
}

// summarizeCommand extracts a brief description from exec args
func summarizeCommand(args map[string]interface{}) string {
	cmd, ok := args["command"].(string)
	if !ok || cmd == "" {
		return "Running command"
	}

	// Trim and get the first meaningful token(s)
	cmd = strings.TrimSpace(cmd)

	// If it's a piped/chained command, just describe the first part
	for _, sep := range []string{" | ", " && ", " ; "} {
		if idx := strings.Index(cmd, sep); idx > 0 {
			cmd = cmd[:idx]
			break
		}
	}

	// Extract the base command name
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "Running command"
	}
	base := parts[0]

	// Map common commands to brief descriptions
	switch base {
	case "ls", "dir":
		return "Listing directory contents"
	case "cd":
		if len(parts) > 1 {
			return fmt.Sprintf("Changing to %s", shortenPath(parts[1]))
		}
		return "Changing directory"
	case "cat", "head", "tail", "less":
		if len(parts) > 1 {
			return fmt.Sprintf("Reading %s", shortenPath(parts[len(parts)-1]))
		}
		return "Reading file contents"
	case "mkdir":
		return "Creating directory"
	case "rm":
		return "Removing files"
	case "cp":
		return "Copying files"
	case "mv":
		return "Moving files"
	case "git":
		if len(parts) > 1 {
			return fmt.Sprintf("Git %s", parts[1])
		}
		return "Running git"
	case "pip", "pip3":
		if len(parts) > 1 {
			return fmt.Sprintf("Pip %s", parts[1])
		}
		return "Running pip"
	case "npm", "yarn", "pnpm":
		if len(parts) > 1 {
			return fmt.Sprintf("%s %s", base, parts[1])
		}
		return fmt.Sprintf("Running %s", base)
	case "make":
		if len(parts) > 1 {
			return fmt.Sprintf("Make %s", parts[1])
		}
		return "Running make"
	case "docker":
		if len(parts) > 1 {
			return fmt.Sprintf("Docker %s", parts[1])
		}
		return "Running docker"
	case "curl", "wget":
		return "Fetching URL"
	case "grep", "rg", "ag":
		return "Searching file contents"
	case "find":
		return "Finding files"
	case "python", "python3":
		if len(parts) > 1 {
			return fmt.Sprintf("Running %s", shortenPath(parts[1]))
		}
		return "Running Python script"
	case "go":
		if len(parts) > 1 {
			return fmt.Sprintf("Go %s", parts[1])
		}
		return "Running go"
	case "apt", "apt-get":
		if len(parts) > 1 {
			return fmt.Sprintf("Apt %s", parts[1])
		}
		return "Running apt"
	case "sudo":
		// Recurse without sudo
		remaining := strings.Join(parts[1:], " ")
		return summarizeCommand(map[string]interface{}{"command": remaining})
	default:
		// For unknown commands, show the command truncated
		return fmt.Sprintf("Running: %s", utils.Truncate(strings.Join(parts[:min(len(parts), 3)], " "), 35))
	}
}

// shortenPath returns just the filename or last path component
func shortenPath(path string) string {
	if path == "" {
		return path
	}
	// Find last slash
	if idx := strings.LastIndex(path, "/"); idx >= 0 && idx < len(path)-1 {
		return path[idx+1:]
	}
	return path
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
