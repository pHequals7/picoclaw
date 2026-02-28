package tools

import (
	"context"
	"fmt"
	"strings"
)

// ToolSearchTool allows the LLM to discover deferred tools by searching.
// When the LLM calls this tool, matching tools are loaded into the active set
// and their definitions will be included in subsequent LLM calls.
type ToolSearchTool struct {
	activeSet *ActiveToolSet
}

// NewToolSearchTool creates a tool_search tool backed by the given ActiveToolSet.
func NewToolSearchTool(activeSet *ActiveToolSet) *ToolSearchTool {
	return &ToolSearchTool{activeSet: activeSet}
}

func (t *ToolSearchTool) Name() string        { return "tool_search" }
func (t *ToolSearchTool) Description() string {
	return "Search for and activate hidden tools by capability. ALWAYS call this before telling the user you cannot perform an action. Many tools (web search, cron, file send, voice, MCP integrations) are hidden until you search for them. After calling this, the discovered tools become available for you to use immediately."
}

func (t *ToolSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query describing the capability you need (e.g., 'web search', 'schedule cron job', 'send file')",
			},
		},
		"required": []string{"query"},
	}
}

func (t *ToolSearchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrorResult("query parameter is required")
	}

	matches := t.activeSet.SearchTools(query)
	if len(matches) == 0 {
		return SilentResult("No tools found matching your query. Try different keywords.")
	}

	// Load all matched tools into the active set
	loaded := 0
	for _, m := range matches {
		if t.activeSet.LoadTool(m.Name) {
			loaded++
		}
	}

	// Format results for the LLM
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d tool(s) matching \"%s\":\n\n", len(matches), query))
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", m.Name, m.Description))
	}
	sb.WriteString(fmt.Sprintf("\n%d tool(s) are now available for use. Call them directly.", loaded))

	return SilentResult(sb.String())
}
