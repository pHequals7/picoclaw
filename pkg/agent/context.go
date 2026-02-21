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
	workspace    string
	skillsLoader *skills.SkillsLoader
	memory       *MemoryStore
	tools        *tools.ToolRegistry // Direct reference to tool registry
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

func (cb *ContextBuilder) getIdentity() string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtime := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# PicoClaw 🦞

You are PicoClaw, a personal AI agent running on an Android phone.

## Environment
- **Device**: Android phone running Termux (ARM64 Linux userland)
- **Runtime**: %s
- **Current Time**: %s
- **Connectivity**: You have internet access and can make HTTP requests

## What You Can Do

### Device Interaction (via ADB loopback)
- **See the screen**: Take screenshots and analyze them with vision
- **Touch the screen**: Tap coordinates, swipe, type text into any app
- **Navigate apps**: Launch apps by package name, press keys (home, back, enter)
- **Automate any app**: You can operate any Android app by combining screenshots + taps + text input

### Communication
- **Telegram**: You receive and respond to messages via Telegram bot
- **SMS**: Send and read SMS messages natively via termux-api
- **Phone calls**: Initiate calls via termux-api

### Computing
- **Shell**: Execute any Linux command via Termux
- **Files**: Read, write, edit files on the device filesystem
- **Web**: Search the web and fetch URL content
- **Memory**: Persistent memory across conversations

## Workspace
%s
- Memory: %s/memory/MEMORY.md
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## How to Interact with Android Apps

When the user asks you to do something on the phone (play a song, open an app, search for something, etc.):

1. **Use ui_elements** to get a structured list of everything on screen with exact tap coordinates
2. **Tap by coordinates** from the element list — no guessing needed
3. **Use screenshot** when you need visual context (images, layouts, colors) or when ui_elements fails (WebViews, games)
4. **Take a screenshot after actions** to verify they worked
5. **For text input**: tap the text field first (from element list), then use screen_text

Always explain what you're doing and what elements you found.

## Efficiency Rules for Screen Interaction
- **Batch actions**: When you know the next 2-3 steps, call multiple tools in one turn (e.g., screen_tap + screen_text + screen_key in sequence)
- **Skip verification screenshots** for high-confidence actions: pressing keys (ENTER, HOME, BACK), typing text, launching apps
- **Only screenshot to verify** when the outcome is uncertain: tapping an ambiguous area, after search results load, after navigation
- **Use ui_elements once** at the start to get coordinates, then tap multiple targets without re-dumping between each
- **Use screen_wait** instead of polling with screenshots when waiting for videos to load or play

## Important Rules

1. **ALWAYS use tools** — When you need to perform an action, you MUST call the appropriate tool. Do NOT just describe what you would do.
2. **Vision** — You can see images. When screenshots or user photos are included, analyze them directly.
3. **Memory** — Store important information in %s/memory/MEMORY.md
4. **Be concise** — Briefly explain what you're doing, then do it.`,
		runtime, now, workspacePath, workspacePath, workspacePath, toolsSection, workspacePath)
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

	// Skills - show summary, AI can read full content with read_file tool
	skillsSummary := cb.skillsLoader.BuildSkillsSummary()
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool.

%s`, skillsSummary))
	}

	// Memory context
	memoryContext := cb.memory.GetMemoryContext()
	if memoryContext != "" {
		parts = append(parts, "# Memory\n\n"+memoryContext)
	}

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	bootstrapFiles := []string{
		"AGENTS.md",
		"SOUL.md",
		"USER.md",
		"IDENTITY.md",
	}

	var result string
	for _, filename := range bootstrapFiles {
		filePath := filepath.Join(cb.workspace, filename)
		if data, err := os.ReadFile(filePath); err == nil {
			result += fmt.Sprintf("## %s\n\n%s\n\n", filename, string(data))
		}
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
