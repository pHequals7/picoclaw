package tools

import (
	"context"
	"testing"
)

// stubTool is a minimal Tool for testing.
type stubTool struct {
	name string
	desc string
}

func (t *stubTool) Name() string                                                    { return t.name }
func (t *stubTool) Description() string                                             { return t.desc }
func (t *stubTool) Parameters() map[string]interface{}                              { return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}} }
func (t *stubTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult { return SilentResult("ok") }

func newTestRegistry() *ToolRegistry {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file"})
	r.Register(&stubTool{name: "exec", desc: "Execute shell command"})
	r.Register(&stubTool{name: "web_search", desc: "Search the web"})
	r.Register(&stubTool{name: "cron", desc: "Schedule recurring tasks"})
	r.Register(&stubTool{name: "file_send", desc: "Send a file to user"})
	return r
}

func TestActiveToolSet_CoreOnly(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})

	// Register tool_search so it can be included
	registry.Register(&stubTool{name: "tool_search", desc: "Search for tools"})

	defs := as.ProviderDefs()

	// Should have read_file, exec, tool_search = 3 core tools
	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Function.Name] = true
	}

	if !names["read_file"] {
		t.Error("expected read_file in defs")
	}
	if !names["exec"] {
		t.Error("expected exec in defs")
	}
	if !names["tool_search"] {
		t.Error("expected tool_search in defs")
	}
	if names["web_search"] {
		t.Error("web_search should be deferred, not in defs")
	}
	if names["cron"] {
		t.Error("cron should be deferred, not in defs")
	}
}

func TestActiveToolSet_LoadDeferred(t *testing.T) {
	registry := newTestRegistry()
	registry.Register(&stubTool{name: "tool_search", desc: "Search for tools"})
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})

	if !as.LoadTool("web_search") {
		t.Fatal("LoadTool(web_search) should succeed")
	}

	defs := as.ProviderDefs()
	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Function.Name] = true
	}

	if !names["web_search"] {
		t.Error("web_search should be in defs after LoadTool")
	}
	if names["cron"] {
		t.Error("cron should still be deferred")
	}
}

func TestActiveToolSet_SearchFindsMatch(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})

	matches := as.SearchTools("web")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match for 'web', got %d", len(matches))
	}
	if matches[0].Name != "web_search" {
		t.Errorf("expected web_search, got %s", matches[0].Name)
	}
}

func TestActiveToolSet_SearchNoMatch(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})

	matches := as.SearchTools("database migration")
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches for 'database migration', got %d", len(matches))
	}
}

func TestActiveToolSet_LoadNonexistent(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file"})

	if as.LoadTool("nonexistent_tool") {
		t.Error("LoadTool should return false for nonexistent tool")
	}
}

func TestActiveToolSet_IsCore(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})

	if !as.IsCore("read_file") {
		t.Error("read_file should be core")
	}
	if !as.IsCore("tool_search") {
		t.Error("tool_search should be implicitly core")
	}
	if as.IsCore("web_search") {
		t.Error("web_search should not be core")
	}
}

func TestActiveToolSet_Reset(t *testing.T) {
	registry := newTestRegistry()
	registry.Register(&stubTool{name: "tool_search", desc: "Search for tools"})
	as := NewActiveToolSet(registry, []string{"read_file"})

	as.LoadTool("web_search")
	as.Reset()

	defs := as.ProviderDefs()
	for _, d := range defs {
		if d.Function.Name == "web_search" {
			t.Error("web_search should not be in defs after Reset")
		}
	}
}
