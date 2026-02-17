package failover

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/state"
)

const (
	modeNormal               = "normal"
	modeDegraded             = "degraded"
	modeAwaitingUserSwitchbk = "awaiting_user_switchback"
)

type Route struct {
	Model       string
	Provider    providers.LLMProvider
	IsPrimary   bool
	Mode        string
	SwitchEpoch int64
}

type SwitchEvent struct {
	FromModel string
	ToModel   string
	Reason    string
	Switched  bool
}

type ProbeOutcome struct {
	Success       bool
	BecameHealthy bool
	PromptText    string
	NextProbeAt   time.Time
}

type DecisionOutcome struct {
	Handled bool
	Reply   string
	Changed bool
}

type Manager struct {
	cfg       *config.Config
	stateMgr  *state.Manager
	mu        sync.Mutex
	fs        state.FailoverState
	primary   string
	fallbacks []string
	providers map[string]providers.LLMProvider
}

func NewManager(cfg *config.Config, stateMgr *state.Manager) *Manager {
	primary := cfg.Agents.Defaults.Model
	fallbacks := normalizeFallbackChain(primary, cfg.Agents.Defaults.FallbackModels, cfg.Agents.Defaults.FallbackModel)
	fs := stateMgr.GetFailoverState()

	if fs.Mode == "" {
		fs.Mode = modeNormal
	}
	if fs.PrimaryModel == "" {
		fs.PrimaryModel = primary
	}
	if fs.ActiveModel == "" {
		fs.ActiveModel = primary
	}
	if fs.FallbackIndex == 0 && fs.ActiveModel == primary {
		fs.FallbackIndex = -1
	}

	m := &Manager{
		cfg:       cfg,
		stateMgr:  stateMgr,
		fs:        fs,
		primary:   primary,
		fallbacks: fallbacks,
		providers: make(map[string]providers.LLMProvider),
	}
	_ = stateMgr.SetFailoverState(fs)
	return m
}

func normalizeFallbackChain(primary string, chain []string, single string) []string {
	if len(chain) == 0 && strings.TrimSpace(single) != "" {
		chain = []string{single}
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(chain))
	for _, model := range chain {
		model = strings.TrimSpace(model)
		if model == "" || model == primary || seen[model] {
			continue
		}
		seen[model] = true
		result = append(result, model)
	}
	return result
}

func (m *Manager) Enabled() bool {
	return m.cfg.Agents.Failover.Enabled
}

func (m *Manager) ResolveRoute() (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	model := m.fs.ActiveModel
	if model == "" {
		model = m.primary
	}
	provider, err := m.providerForModelLocked(model)
	if err != nil {
		return Route{}, err
	}

	return Route{
		Model:       model,
		Provider:    provider,
		IsPrimary:   model == m.primary,
		Mode:        m.fs.Mode,
		SwitchEpoch: m.fs.SwitchEpoch,
	}, nil
}

func (m *Manager) providerForModelLocked(model string) (providers.LLMProvider, error) {
	if p, ok := m.providers[model]; ok {
		return p, nil
	}
	p, err := providers.CreateProviderForModel(m.cfg, model)
	if err != nil {
		return nil, err
	}
	m.providers[model] = p
	return p, nil
}

func (m *Manager) SetProviderForModel(model string, provider providers.LLMProvider) {
	if model == "" || provider == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[model] = provider
}

func (m *Manager) OnLLMRateLimited(model string, err error) SwitchEvent {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.Enabled() {
		return SwitchEvent{Switched: false}
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	m.fs.LastRateLimitError = errMsg

	from := m.fs.ActiveModel
	if from == "" {
		from = model
	}
	if from == "" {
		from = m.primary
	}

	if len(m.fallbacks) == 0 {
		m.fs.LastSwitchReason = "rate_limited_no_fallback"
		m.persistLocked()
		return SwitchEvent{FromModel: from, ToModel: from, Reason: "no_fallback_configured", Switched: false}
	}

	now := time.Now()
	holdUntil := now.Add(time.Duration(maxInt(m.cfg.Agents.Failover.HoldMinutes, 1)) * time.Minute)
	if rl, ok := err.(*providers.RateLimitError); ok {
		if hinted := nextProbeFromRateLimitHints(now, rl); hinted.After(holdUntil) {
			holdUntil = hinted
		}
	}

	var to string
	if from == m.primary {
		m.fs.FallbackIndex = 0
		to = m.fallbacks[0]
	} else {
		next := m.fs.FallbackIndex + 1
		if next < 0 {
			next = 0
		}
		if next >= len(m.fallbacks) {
			m.fs.LastSwitchReason = "rate_limited_fallback_exhausted"
			m.persistLocked()
			return SwitchEvent{FromModel: from, ToModel: from, Reason: "fallback_exhausted", Switched: false}
		}
		m.fs.FallbackIndex = next
		to = m.fallbacks[next]
	}

	m.fs.Mode = modeDegraded
	m.fs.ActiveModel = to
	m.fs.PrimaryModel = m.primary
	m.fs.DegradedAt = now
	m.fs.HoldUntil = holdUntil
	m.fs.NextProbeAt = holdUntil
	m.fs.ConsecutiveProbeSuccesses = 0
	m.fs.LastSwitchReason = "rate_limited"
	m.fs.SwitchEpoch++
	m.persistLocked()

	return SwitchEvent{FromModel: from, ToModel: to, Reason: "rate_limited", Switched: true}
}

func (m *Manager) OnLLMSuccess(model string) {
	if !m.Enabled() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fs.ActiveModel == "" {
		m.fs.ActiveModel = model
		m.persistLocked()
	}
}

func (m *Manager) ShouldProbe(now time.Time) bool {
	if !m.Enabled() {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.fs.ActiveModel == "" || m.fs.ActiveModel == m.primary {
		return false
	}
	if now.Before(m.fs.HoldUntil) {
		return false
	}
	return m.fs.NextProbeAt.IsZero() || !now.Before(m.fs.NextProbeAt)
}

func (m *Manager) RunProbe(ctx context.Context) ProbeOutcome {
	m.mu.Lock()
	primary := m.primary
	m.mu.Unlock()

	provider, err := providers.CreateProviderForModel(m.cfg, primary)
	if err != nil {
		return m.recordProbeResult(false, err)
	}

	_, err = provider.Chat(ctx,
		[]providers.Message{{Role: "user", Content: "health_check: reply with OK"}},
		nil,
		primary,
		map[string]interface{}{"max_tokens": 8, "temperature": 0.0},
	)
	if err != nil {
		return m.recordProbeResult(false, err)
	}
	return m.recordProbeResult(true, nil)
}

func (m *Manager) recordProbeResult(success bool, err error) ProbeOutcome {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	interval := time.Duration(maxInt(m.cfg.Agents.Failover.ProbeIntervalMinutes, 1)) * time.Minute
	backoff := time.Duration(maxInt(m.cfg.Agents.Failover.ProbeFailureBackoffMinutes, 1)) * time.Minute
	hold := time.Duration(maxInt(m.cfg.Agents.Failover.HoldMinutes, 1)) * time.Minute
	threshold := maxInt(m.cfg.Agents.Failover.ProbeSuccessThreshold, 1)

	m.fs.LastProbeAt = now

	if success {
		m.fs.ConsecutiveProbeSuccesses++
		m.fs.LastSwitchbackProbe = fmt.Sprintf("%d/%d successful probes as of %s", m.fs.ConsecutiveProbeSuccesses, threshold, now.Format(time.RFC3339))
		m.fs.NextProbeAt = now.Add(interval)
		prompt := ""
		if m.fs.ConsecutiveProbeSuccesses >= threshold {
			m.fs.Mode = modeAwaitingUserSwitchbk
			if m.cfg.Agents.Failover.SwitchbackRequiresApproval {
				prompt = m.buildSwitchbackPromptLocked(now)
			}
		}
		m.persistLocked()
		return ProbeOutcome{Success: true, BecameHealthy: m.fs.ConsecutiveProbeSuccesses >= threshold, PromptText: prompt, NextProbeAt: m.fs.NextProbeAt}
	}

	m.fs.ConsecutiveProbeSuccesses = 0
	m.fs.Mode = modeDegraded
	m.fs.LastSwitchbackProbe = ""
	m.fs.NextProbeAt = now.Add(backoff)
	if rl, ok := err.(*providers.RateLimitError); ok {
		next := now.Add(hold)
		if hinted := nextProbeFromRateLimitHints(now, rl); hinted.After(next) {
			next = hinted
		}
		m.fs.HoldUntil = next
		m.fs.NextProbeAt = next
	}
	m.persistLocked()
	return ProbeOutcome{Success: false, NextProbeAt: m.fs.NextProbeAt}
}

func nextProbeFromRateLimitHints(now time.Time, rl *providers.RateLimitError) time.Time {
	candidates := []time.Time{}
	if rl == nil {
		return time.Time{}
	}
	for _, raw := range []string{rl.RetryAfter, rl.RateLimitRequestsReset, rl.RateLimitTokensReset} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if secs, err := strconv.Atoi(raw); err == nil {
			if strings.EqualFold(raw, rl.RetryAfter) {
				candidates = append(candidates, now.Add(time.Duration(secs)*time.Second))
			} else {
				candidates = append(candidates, time.Unix(int64(secs), 0))
			}
			continue
		}
		if t, err := httpDateOrRFC3339(raw); err == nil {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return time.Time{}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Before(candidates[j]) })
	return candidates[len(candidates)-1]
}

func httpDateOrRFC3339(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, v)
}

func (m *Manager) buildSwitchbackPromptLocked(now time.Time) string {
	return fmt.Sprintf(
		"Primary model %s looks healthy (%s). Currently using fallback %s. Reply 'yes' to switch back to primary or 'no' to stay on fallback.",
		m.primary,
		m.fs.LastSwitchbackProbe,
		m.fs.ActiveModel,
	)
}

func (m *Manager) ShouldSendSwitchbackPrompt(now time.Time) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fs.Mode != modeAwaitingUserSwitchbk {
		return "", false
	}

	cooldown := time.Duration(maxInt(m.cfg.Agents.Failover.SwitchbackPromptCooldownMins, 1)) * time.Minute
	if m.fs.LastSwitchbackPromptAt.IsZero() || now.Sub(m.fs.LastSwitchbackPromptAt) >= cooldown {
		m.fs.LastSwitchbackPromptAt = now
		m.persistLocked()
		return m.buildSwitchbackPromptLocked(now), true
	}
	return "", false
}

func (m *Manager) HandleUserSwitchbackDecision(text string) DecisionOutcome {
	if !m.Enabled() {
		return DecisionOutcome{}
	}

	normalized := strings.ToLower(strings.TrimSpace(text))
	isYes := normalized == "yes" || normalized == "y" || normalized == "switch" || normalized == "switch back"
	isNo := normalized == "no" || normalized == "n" || normalized == "stay" || normalized == "stay fallback"
	if !isYes && !isNo {
		return DecisionOutcome{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.fs.Mode != modeAwaitingUserSwitchbk {
		return DecisionOutcome{}
	}

	now := time.Now()
	if isYes {
		oldActive := m.fs.ActiveModel
		m.fs.Mode = modeNormal
		m.fs.ActiveModel = m.primary
		m.fs.FallbackIndex = -1
		m.fs.ConsecutiveProbeSuccesses = 0
		m.fs.LastSwitchReason = "manual_switchback_approved"
		m.fs.LastSwitchbackPromptAt = time.Time{}
		m.fs.LastSwitchbackProbe = ""
		m.fs.SwitchEpoch++
		m.persistLocked()
		return DecisionOutcome{Handled: true, Changed: true, Reply: fmt.Sprintf("Switched back to primary model %s from %s.", m.primary, oldActive)}
	}

	cooldown := time.Duration(maxInt(m.cfg.Agents.Failover.SwitchbackPromptCooldownMins, 1)) * time.Minute
	m.fs.LastSwitchReason = "manual_switchback_declined"
	m.fs.LastSwitchbackPromptAt = now.Add(-cooldown + time.Second)
	m.persistLocked()
	return DecisionOutcome{Handled: true, Changed: false, Reply: fmt.Sprintf("Staying on fallback model %s. I will remind you again later if primary stays healthy.", m.fs.ActiveModel)}
}

func (m *Manager) IsUsingPrimary() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fs.ActiveModel == "" {
		return true
	}
	return m.fs.ActiveModel == m.primary
}

func (m *Manager) Snapshot() state.FailoverState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fs
}

func (m *Manager) persistLocked() {
	_ = m.stateMgr.SetFailoverState(m.fs)
}

func (m *Manager) PrimaryModel() string {
	return m.primary
}

func (m *Manager) ActiveModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fs.ActiveModel == "" {
		return m.primary
	}
	return m.fs.ActiveModel
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
