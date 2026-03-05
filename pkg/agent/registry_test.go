package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestAgentRegistry_RegisterAndGet(t *testing.T) {
	reg := NewAgentRegistry()

	inst := &AgentInstance{
		ID:    "worker",
		Tools: tools.NewToolRegistry(),
	}
	reg.Register(inst)

	got, ok := reg.Get("worker")
	if !ok {
		t.Fatal("expected to find 'worker'")
	}
	if got.ID != "worker" {
		t.Errorf("ID = %q, want 'worker'", got.ID)
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected not to find 'nonexistent'")
	}
}

func TestAgentRegistry_Default(t *testing.T) {
	reg := NewAgentRegistry()

	// No default set
	_, err := reg.Default()
	if err == nil {
		t.Error("expected error when no default set")
	}

	inst := &AgentInstance{
		ID:    "main",
		Tools: tools.NewToolRegistry(),
	}
	reg.Register(inst)
	reg.SetDefault("main")

	got, err := reg.Default()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "main" {
		t.Errorf("Default().ID = %q, want 'main'", got.ID)
	}
}

func TestAgentRegistry_List(t *testing.T) {
	reg := NewAgentRegistry()

	reg.Register(&AgentInstance{ID: "a"})
	reg.Register(&AgentInstance{ID: "b"})
	reg.Register(&AgentInstance{ID: "c"})

	ids := reg.List()
	if len(ids) != 3 {
		t.Errorf("expected 3 agents, got %d", len(ids))
	}
	if reg.Count() != 3 {
		t.Errorf("Count() = %d, want 3", reg.Count())
	}
}

func TestAgentRouting_DefaultAgent(t *testing.T) {
	reg := NewAgentRegistry()
	defaultInst := &AgentInstance{ID: "default"}
	reg.Register(defaultInst)
	reg.SetDefault("default")

	// Create a minimal AgentLoop to test resolveAgent
	al := &AgentLoop{agentRegistry: reg}

	inst, content := al.resolveAgent("Hello, how are you?")
	if inst == nil {
		t.Fatal("expected non-nil instance")
	}
	if inst.ID != "default" {
		t.Errorf("resolved agent ID = %q, want 'default'", inst.ID)
	}
	if content != "Hello, how are you?" {
		t.Errorf("content = %q, want original", content)
	}
}

func TestAgentRouting_ExplicitAgent(t *testing.T) {
	reg := NewAgentRegistry()
	reg.Register(&AgentInstance{ID: "default"})
	reg.Register(&AgentInstance{ID: "coder"})
	reg.SetDefault("default")

	al := &AgentLoop{agentRegistry: reg}

	inst, content := al.resolveAgent("@coder write a function")
	if inst == nil {
		t.Fatal("expected non-nil instance")
	}
	if inst.ID != "coder" {
		t.Errorf("resolved agent ID = %q, want 'coder'", inst.ID)
	}
	if content != "write a function" {
		t.Errorf("content = %q, want 'write a function'", content)
	}
}

func TestAgentRouting_UnknownAgentFallsToDefault(t *testing.T) {
	reg := NewAgentRegistry()
	reg.Register(&AgentInstance{ID: "default"})
	reg.SetDefault("default")

	al := &AgentLoop{agentRegistry: reg}

	inst, content := al.resolveAgent("@unknown do something")
	if inst == nil {
		t.Fatal("expected non-nil instance")
	}
	if inst.ID != "default" {
		t.Errorf("resolved agent ID = %q, want 'default' (fallback)", inst.ID)
	}
	// Content unchanged when agent not found
	if content != "@unknown do something" {
		t.Errorf("content = %q, want original (unchanged)", content)
	}
}
