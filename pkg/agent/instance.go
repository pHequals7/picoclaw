package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// AgentInstanceConfig holds per-instance configuration.
type AgentInstanceConfig struct {
	ID                string
	Model             string
	MaxIterations     int
	SystemPromptExtra string
	AllowedTools      []string
	Workspace         string
	ParallelTools     bool
	MaxMediaSize      int64
}

// AgentInstance represents a named agent with its own executor and tools.
type AgentInstance struct {
	ID         string
	Executor   *LLMExecutor
	Tools      *tools.ToolRegistry
	MediaStore media.MediaStore
	Config     AgentInstanceConfig
}

// Run executes the agent's pipeline-based loop with the given messages.
func (ai *AgentInstance) Run(ctx context.Context, messages []providers.Message, opts processOptions) IterationResult {
	return ai.Executor.Run(ctx, messages, opts)
}
