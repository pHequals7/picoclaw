package agent

import (
	"fmt"
	"sync"
)

// AgentRegistry manages named agent instances.
type AgentRegistry struct {
	mu        sync.RWMutex
	instances map[string]*AgentInstance
	defaultID string
}

// NewAgentRegistry creates an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		instances: make(map[string]*AgentInstance),
	}
}

// Register adds an agent instance to the registry.
func (r *AgentRegistry) Register(instance *AgentInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[instance.ID] = instance
}

// SetDefault designates which agent ID is the default.
func (r *AgentRegistry) SetDefault(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultID = id
}

// Get returns an agent instance by ID.
func (r *AgentRegistry) Get(id string) (*AgentInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inst, ok := r.instances[id]
	return inst, ok
}

// Default returns the default agent instance.
func (r *AgentRegistry) Default() (*AgentInstance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.defaultID == "" {
		return nil, fmt.Errorf("no default agent configured")
	}
	inst, ok := r.instances[r.defaultID]
	if !ok {
		return nil, fmt.Errorf("default agent %q not found in registry", r.defaultID)
	}
	return inst, nil
}

// List returns all registered agent IDs.
func (r *AgentRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.instances))
	for id := range r.instances {
		ids = append(ids, id)
	}
	return ids
}

// Count returns the number of registered agents.
func (r *AgentRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.instances)
}
