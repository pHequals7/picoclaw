package tools

import (
	"context"
	"strings"
	"testing"
)

func TestToolSearchTool_BasicSearch(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})
	searchTool := NewToolSearchTool(as)

	result := searchTool.Execute(context.Background(), map[string]interface{}{
		"query": "schedule recurring",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "cron") {
		t.Errorf("expected result to mention cron, got: %s", result.ForLLM)
	}

	// Verify cron was loaded
	defs := as.ProviderDefs()
	found := false
	for _, d := range defs {
		if d.Function.Name == "cron" {
			found = true
			break
		}
	}
	if !found {
		t.Error("cron should be loaded after search")
	}
}

func TestToolSearchTool_NoResults(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})
	searchTool := NewToolSearchTool(as)

	result := searchTool.Execute(context.Background(), map[string]interface{}{
		"query": "quantum computing",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "No tools found") {
		t.Errorf("expected 'No tools found' message, got: %s", result.ForLLM)
	}
}

func TestToolSearchTool_EmptyQuery(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file"})
	searchTool := NewToolSearchTool(as)

	result := searchTool.Execute(context.Background(), map[string]interface{}{
		"query": "",
	})

	if !result.IsError {
		t.Error("expected error for empty query")
	}
}

func TestToolSearchTool_Parameters(t *testing.T) {
	registry := newTestRegistry()
	as := NewActiveToolSet(registry, []string{"read_file"})
	searchTool := NewToolSearchTool(as)

	params := searchTool.Parameters()
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties in parameters")
	}
	if _, ok := props["query"]; !ok {
		t.Fatal("expected 'query' in properties")
	}

	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("expected required array")
	}
	if len(required) != 1 || required[0] != "query" {
		t.Errorf("expected required=[query], got %v", required)
	}
}

func TestToolSearchTool_MultipleMatches(t *testing.T) {
	registry := newTestRegistry()
	// Add another tool with "file" in its name/desc
	registry.Register(&stubTool{name: "file_upload", desc: "Upload a file to cloud"})
	as := NewActiveToolSet(registry, []string{"read_file", "exec"})
	searchTool := NewToolSearchTool(as)

	result := searchTool.Execute(context.Background(), map[string]interface{}{
		"query": "file",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	// Should match file_send and file_upload (not read_file since it's core)
	if !strings.Contains(result.ForLLM, "file_send") {
		t.Errorf("expected file_send in results: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "file_upload") {
		t.Errorf("expected file_upload in results: %s", result.ForLLM)
	}
}
