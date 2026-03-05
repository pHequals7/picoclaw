package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type ContextBuilder struct {
	workspace       string
	skillsLoader    *skills.SkillsLoader
	memory          *MemoryStore
	tools           *tools.ToolRegistry // Direct reference to tool registry
	toolSearchHint  bool                // true = append tool_search guidance
}

func getGlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".picoclaw")
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		memory:       NewMemoryStore(workspace),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

// SetToolSearchEnabled enables the tool search hint in the system prompt.
func (cb *ContextBuilder) SetToolSearchEnabled(enabled bool) {
	cb.toolSearchHint = enabled
}

func (cb *ContextBuilder) getIdentity() string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtime := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# picoclaw 🦞

You are picoclaw, a helpful AI assistant.

## Current Time
%s

## Runtime
%s

## Workspace
Your workspace is at: %s
- Memory: %s/memory/MEMORY.md
- Daily Notes: %s/memory/YYYYMM/YYYYMMDD.md
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Important Rules

1. **ALWAYS use tools** - When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

3. **Memory** - When remembering something, write to %s/memory/MEMORY.md

4. **Vision** - You can see images. When users send photos, the images are included in the message as base64-encoded data. Describe, analyze, or answer questions about them directly — do NOT say you cannot see images.`,
		now, runtime, workspacePath, workspacePath, workspacePath, workspacePath, toolsSection, workspacePath)
}

func (cb *ContextBuilder) buildToolsSection() string {
	if cb.tools == nil {
		return ""
	}

	summaries := cb.tools.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands or schedule tasks.\n\n")
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	if cb.toolSearchHint {
		sb.WriteString(`
### Tool Search

**IMPORTANT**: The tools listed above are only your core tools. Many more tools are available but hidden to save context. You MUST use the ` + "`tool_search`" + ` tool to discover them.

**When to use tool_search:**
- Before ANY task involving: web search, web browsing, scheduling/cron, sending files, voice, spawning sub-agents, or importing attachments
- Whenever a user asks you to do something and none of your visible tools can handle it
- NEVER tell the user "I don't have a tool for that" without first searching for one

**How to use it:** Call ` + "`tool_search`" + ` with a short descriptive query like "web search", "schedule cron", "send file", or "voice transcription". The matching tools will become available for you to call immediately.

**Available categories of hidden tools:** web search & fetch, cron/scheduling, file sending, attachment import, voice transcription, sub-agent spawning, and any connected MCP server tools.
`)
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	parts := []string{}

	// Core identity section
	parts = append(parts, cb.getIdentity())

	// Bootstrap files
	bootstrapContent := cb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// Skills - compact name list (agent reads SKILL.md on demand)
	allSkills := cb.skillsLoader.ListSkills()
	if len(allSkills) > 0 {
		var names []string
		for _, s := range allSkills {
			names = append(names, s.Name)
		}
		parts = append(parts, fmt.Sprintf("# Skills: %s\nUse `read_file` to load any skill's SKILL.md when needed.",
			strings.Join(names, ", ")))
	}

	// Memory context — brief excerpt (agent can read_file for full content)
	memoryContext := cb.memory.GetMemoryContext()
	if memoryContext != "" {
		const maxMemoryChars = 500
		if len(memoryContext) > maxMemoryChars {
			memoryContext = memoryContext[:maxMemoryChars] + "\n...[use read_file for full memory]"
		}
		parts = append(parts, "# Memory\n\n"+memoryContext)
	}

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	// Always load SOUL.md inline (tiny, defines personality)
	var result string
	soulPath := filepath.Join(cb.workspace, "SOUL.md")
	if data, err := os.ReadFile(soulPath); err == nil {
		result += fmt.Sprintf("## SOUL.md\n\n%s\n\n", string(data))
	}

	// Other bootstrap files: just list paths so the agent can read_file on demand
	referenceFiles := []struct {
		name string
		desc string
	}{
		{"AGENTS.md", "credential policies, model routing"},
		{"USER.md", "user info, preferences, environment"},
		{"IDENTITY.md", "bot identity and capabilities"},
	}

	var refs []string
	for _, rf := range referenceFiles {
		filePath := filepath.Join(cb.workspace, rf.name)
		if _, err := os.Stat(filePath); err == nil {
			refs = append(refs, fmt.Sprintf("- %s (%s)", filePath, rf.desc))
		}
	}
	if len(refs) > 0 {
		result += "## Reference Documents\nUse `read_file` to access these when needed:\n" + strings.Join(refs, "\n") + "\n\n"
	}

	return result
}

func (cb *ContextBuilder) BuildMessages(history []providers.Message, summary string, currentMessage string, media []string, channel, chatID string) []providers.Message {
	messages := []providers.Message{}

	systemPrompt := cb.BuildSystemPrompt()

	// Add Current Session info if provided
	if channel != "" && chatID != "" {
		systemPrompt += fmt.Sprintf("\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}

	// Log system prompt summary for debugging (debug mode only)
	logger.DebugCF("agent", "System prompt built",
		map[string]interface{}{
			"total_chars":   len(systemPrompt),
			"total_lines":   strings.Count(systemPrompt, "\n") + 1,
			"section_count": strings.Count(systemPrompt, "\n\n---\n\n") + 1,
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := systemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "System prompt preview",
		map[string]interface{}{
			"preview": preview,
		})

	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	//This fix prevents the session memory from LLM failure due to elimination of toolu_IDs required from LLM
	// --- INICIO DEL FIX ---
	//Diegox-17
	for len(history) > 0 && (history[0].Role == "tool") {
		logger.DebugCF("agent", "Removing orphaned tool message from history to prevent LLM error",
			map[string]interface{}{"role": history[0].Role})
		history = history[1:]
	}
	//Diegox-17
	// --- FIN DEL FIX ---

	messages = append(messages, providers.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	messages = append(messages, history...)

	currentMsg := providers.Message{Role: "user", Content: currentMessage}
	if len(media) > 0 {
		images := utils.ProcessMediaImages(media)
		if len(images) > 0 {
			currentMsg.Media = make([]providers.MediaImage, len(images))
			for i, img := range images {
				currentMsg.Media[i] = providers.MediaImage{
					MimeType:   img.MimeType,
					Base64Data: img.Base64Data,
				}
			}
			logger.InfoCF("agent", "Attached images to message",
				map[string]interface{}{"count": len(images)})
		}
	}
	messages = append(messages, currentMsg)

	return messages
}

func (cb *ContextBuilder) AddToolResult(messages []providers.Message, toolCallID, toolName, result string) []providers.Message {
	messages = append(messages, providers.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return messages
}

func (cb *ContextBuilder) AddAssistantMessage(messages []providers.Message, content string, toolCalls []map[string]interface{}) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	messages = append(messages, msg)
	return messages
}

func (cb *ContextBuilder) loadSkills() string {
	allSkills := cb.skillsLoader.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	var skillNames []string
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}

	content := cb.skillsLoader.LoadSkillsForContext(skillNames)
	if content == "" {
		return ""
	}

	return "# Skill Definitions\n\n" + content
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]interface{} {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]interface{}{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}
