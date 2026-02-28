package tools

import (
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// ToolSummary is a lightweight description of a tool returned by search.
type ToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ActiveToolSet tracks which tools are visible to the LLM in the current conversation.
// Core tools are always visible. Deferred tools become visible after tool_search finds them.
type ActiveToolSet struct {
	registry  *ToolRegistry
	coreNames map[string]struct{}
	loaded    map[string]struct{}
	mu        sync.RWMutex
}

// NewActiveToolSet creates an ActiveToolSet. Core tool names are always included
// in provider definitions. The tool_search tool is implicitly added as core.
func NewActiveToolSet(registry *ToolRegistry, coreNames []string) *ActiveToolSet {
	core := make(map[string]struct{}, len(coreNames)+1)
	for _, name := range coreNames {
		core[name] = struct{}{}
	}
	core["tool_search"] = struct{}{}

	return &ActiveToolSet{
		registry:  registry,
		coreNames: core,
		loaded:    make(map[string]struct{}),
	}
}

// ProviderDefs returns tool definitions for core + currently-loaded tools.
func (a *ActiveToolSet) ProviderDefs() []providers.ToolDefinition {
	a.mu.RLock()
	visible := make(map[string]struct{}, len(a.coreNames)+len(a.loaded))
	for k := range a.coreNames {
		visible[k] = struct{}{}
	}
	for k := range a.loaded {
		visible[k] = struct{}{}
	}
	a.mu.RUnlock()

	return a.registry.ToProviderDefsFiltered(visible)
}

// LoadTool marks a deferred tool as active for this conversation.
// Returns true if the tool exists in the registry.
func (a *ActiveToolSet) LoadTool(name string) bool {
	if _, ok := a.registry.Get(name); !ok {
		return false
	}
	a.mu.Lock()
	a.loaded[name] = struct{}{}
	a.mu.Unlock()
	return true
}

// SearchTools finds deferred tools matching a query string.
// Uses case-insensitive substring matching on tool name and description.
func (a *ActiveToolSet) SearchTools(query string) []ToolSummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	queryLower := strings.ToLower(query)
	queryWords := strings.Fields(queryLower)

	a.registry.mu.RLock()
	defer a.registry.mu.RUnlock()

	var matches []ToolSummary
	for _, tool := range a.registry.tools {
		name := tool.Name()
		// Skip core tools — they're already visible
		if _, isCore := a.coreNames[name]; isCore {
			continue
		}
		// Skip already-loaded tools
		if _, isLoaded := a.loaded[name]; isLoaded {
			continue
		}

		nameLower := strings.ToLower(name)
		descLower := strings.ToLower(tool.Description())
		searchable := nameLower + " " + descLower

		// Match if any query word appears in name or description
		for _, word := range queryWords {
			if strings.Contains(searchable, word) {
				matches = append(matches, ToolSummary{
					Name:        name,
					Description: tool.Description(),
				})
				break
			}
		}
	}
	return matches
}

// IsCore returns true if the tool is in the always-visible set.
func (a *ActiveToolSet) IsCore(name string) bool {
	_, ok := a.coreNames[name]
	return ok
}

// Reset clears the loaded deferred tools, restoring to core-only state.
func (a *ActiveToolSet) Reset() {
	a.mu.Lock()
	a.loaded = make(map[string]struct{})
	a.mu.Unlock()
}
