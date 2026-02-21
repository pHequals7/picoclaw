package failover

import (
	"os"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/state"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	tmp, err := os.MkdirTemp("", "failover-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "claude-sonnet-4-5-20250929"
	cfg.Agents.Defaults.FallbackModels = []string{"gpt-5-mini", "gemini-2.5-flash"}
	cfg.Agents.Failover.Enabled = true
	cfg.Agents.Failover.HoldMinutes = 300
	cfg.Agents.Failover.ProbeSuccessThreshold = 2
	cfg.Agents.Failover.ProbeIntervalMinutes = 5
	cfg.Agents.Failover.ProbeFailureBackoffMinutes = 10

	sm := state.NewManager(tmp)
	return NewManager(cfg, sm)
}

func TestOnLLMRateLimitedSwitchesToFirstFallback(t *testing.T) {
	m := newTestManager(t)
	evt := m.OnLLMRateLimited(m.PrimaryModel(), nil)
	if !evt.Switched {
		t.Fatalf("expected switch event")
	}
	if evt.ToModel != "gpt-5-mini" {
		t.Fatalf("expected first fallback, got %s", evt.ToModel)
	}
}

func TestOnLLMRateLimitedAdvancesFallbackChain(t *testing.T) {
	m := newTestManager(t)
	_ = m.OnLLMRateLimited(m.PrimaryModel(), nil)
	evt := m.OnLLMRateLimited("gpt-5-mini", nil)
	if !evt.Switched {
		t.Fatalf("expected second switch")
	}
	if evt.ToModel != "gemini-2.5-flash" {
		t.Fatalf("expected second fallback, got %s", evt.ToModel)
	}
}

func TestConsumeSwitchbackPrompt_OneShot(t *testing.T) {
	m := newTestManager(t)
	_ = m.OnLLMRateLimited(m.PrimaryModel(), nil)
	m.mu.Lock()
	m.fs.Mode = modeAwaitingUserSwitchbk
	m.fs.LastSwitchbackProbe = "healthy"
	m.fs.SwitchbackPromptSent = false
	m.mu.Unlock()

	now := time.Now()
	if _, ok := m.ConsumeSwitchbackPrompt(now); !ok {
		t.Fatalf("expected first prompt")
	}
	if _, ok := m.ConsumeSwitchbackPrompt(now.Add(1 * time.Minute)); ok {
		t.Fatalf("did not expect repeated prompt in same failover cycle")
	}
}

func TestHandleUserDecisionSwitchesBack(t *testing.T) {
	m := newTestManager(t)
	_ = m.OnLLMRateLimited(m.PrimaryModel(), nil)
	m.mu.Lock()
	m.fs.Mode = modeAwaitingUserSwitchbk
	m.mu.Unlock()

	outcome := m.HandleUserSwitchbackDecision("yes")
	if !outcome.Handled || !outcome.Changed {
		t.Fatalf("expected handled+changed outcome")
	}
	if !m.IsUsingPrimary() {
		t.Fatalf("expected to return to primary")
	}
}

func TestProbeAutoSwitchbackWithoutApproval(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Agents.Failover.SwitchbackRequiresApproval = false
	_ = m.OnLLMRateLimited(m.PrimaryModel(), nil)

	_ = m.recordProbeResult(true, nil)
	outcome := m.recordProbeResult(true, nil)
	if !outcome.BecameHealthy {
		t.Fatalf("expected probe threshold to mark healthy")
	}
	if !m.IsUsingPrimary() {
		t.Fatalf("expected automatic switchback to primary")
	}
	if snap := m.Snapshot(); snap.Mode != modeNormal {
		t.Fatalf("expected normal mode after auto switchback, got %s", snap.Mode)
	}
}

func TestNewFailoverCycleResetsPromptSent(t *testing.T) {
	m := newTestManager(t)
	_ = m.OnLLMRateLimited(m.PrimaryModel(), nil)
	m.mu.Lock()
	m.fs.Mode = modeAwaitingUserSwitchbk
	m.fs.SwitchbackPromptSent = true
	m.mu.Unlock()

	evt := m.OnLLMRateLimited("gpt-5-mini", nil)
	if !evt.Switched {
		t.Fatalf("expected switch to next fallback")
	}
	if snap := m.Snapshot(); snap.SwitchbackPromptSent {
		t.Fatalf("expected switchback prompt sent flag reset in new failover cycle")
	}
}
