package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	stateVersion  = 1
	retentionDays = 30
)

type Record struct {
	Timestamp        time.Time `json:"timestamp"`
	DayKey           string    `json:"day_key"`
	SessionKey       string    `json:"session_key,omitempty"`
	Channel          string    `json:"channel,omitempty"`
	ChatID           string    `json:"chat_id,omitempty"`
	CorrelationID    string    `json:"correlation_id,omitempty"`
	Iteration        int       `json:"iteration,omitempty"`
	CallIndex        int       `json:"call_index,omitempty"`
	Provider         string    `json:"provider,omitempty"`
	Model            string    `json:"model,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens,omitempty"`
	UsageKnown       bool      `json:"usage_known"`
	Reason           string    `json:"reason,omitempty"`
	FinishReason     string    `json:"finish_reason,omitempty"`
}

type Aggregate struct {
	Calls            int `json:"calls"`
	KnownCalls       int `json:"known_calls"`
	UnknownCalls     int `json:"unknown_calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Filter struct {
	SessionKey string
	DayKey     string
	Provider   string
	Limit      int
}

type usageState struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

type Store struct {
	mu        sync.RWMutex
	path      string
	state     usageState
	loc       *time.Location
	retention int
}

func NewStore(workspace string) *Store {
	stateDir := filepath.Join(workspace, "state")
	_ = os.MkdirAll(stateDir, 0755)

	loc := time.FixedZone("IST", 5*3600+30*60)
	if l, err := time.LoadLocation("Asia/Kolkata"); err == nil {
		loc = l
	}

	s := &Store{
		path:      filepath.Join(stateDir, "usage.json"),
		state:     usageState{Version: stateVersion, Records: []Record{}},
		loc:       loc,
		retention: retentionDays,
	}
	_ = s.load()
	_ = s.pruneAndSaveLocked(time.Now())
	return s
}

func (s *Store) TodayKey() string {
	return s.DayKey(time.Now())
}

func (s *Store) DayKey(ts time.Time) string {
	return ts.In(s.loc).Format("2006-01-02")
}

func (s *Store) Append(record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if record.Timestamp.IsZero() {
		record.Timestamp = now
	}
	if record.DayKey == "" {
		record.DayKey = s.DayKey(record.Timestamp)
	}
	if record.TotalTokens == 0 && (record.PromptTokens > 0 || record.CompletionTokens > 0) {
		record.TotalTokens = record.PromptTokens + record.CompletionTokens
	}

	s.state.Records = append(s.state.Records, record)
	return s.pruneAndSaveLocked(now)
}

func (s *Store) LastBySession(sessionKey string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := len(s.state.Records) - 1; i >= 0; i-- {
		r := s.state.Records[i]
		if r.SessionKey == sessionKey {
			return r, true
		}
	}
	return Record{}, false
}

func (s *Store) Query(filter Filter) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]Record, 0, len(s.state.Records))
	for _, r := range s.state.Records {
		if filter.SessionKey != "" && r.SessionKey != filter.SessionKey {
			continue
		}
		if filter.DayKey != "" && r.DayKey != filter.DayKey {
			continue
		}
		if filter.Provider != "" && r.Provider != filter.Provider {
			continue
		}
		matched = append(matched, r)
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Timestamp.After(matched[j].Timestamp)
	})

	if filter.Limit > 0 && len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}
	return matched
}

func AggregateRecords(records []Record) Aggregate {
	var out Aggregate
	for _, r := range records {
		out.Calls++
		if r.UsageKnown {
			out.KnownCalls++
			out.PromptTokens += r.PromptTokens
			out.CompletionTokens += r.CompletionTokens
			out.TotalTokens += r.TotalTokens
		} else {
			out.UnknownCalls++
		}
	}
	return out
}

func ProviderBreakdown(records []Record) map[string]Aggregate {
	out := map[string]Aggregate{}
	for _, r := range records {
		provider := r.Provider
		if provider == "" {
			provider = "unknown"
		}
		agg := out[provider]
		agg.Calls++
		if r.UsageKnown {
			agg.KnownCalls++
			agg.PromptTokens += r.PromptTokens
			agg.CompletionTokens += r.CompletionTokens
			agg.TotalTokens += r.TotalTokens
		} else {
			agg.UnknownCalls++
		}
		out[provider] = agg
	}
	return out
}

func (s *Store) pruneAndSaveLocked(now time.Time) error {
	cutoff := now.AddDate(0, 0, -s.retention)
	filtered := make([]Record, 0, len(s.state.Records))
	for _, r := range s.state.Records {
		if r.Timestamp.Before(cutoff) {
			continue
		}
		filtered = append(filtered, r)
	}
	s.state.Version = stateVersion
	s.state.Records = filtered
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal usage state: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write usage temp file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename usage temp file: %w", err)
	}
	return nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var st usageState
	if err := json.Unmarshal(data, &st); err != nil {
		// Corrupt usage state should not block runtime; reset in-memory state.
		s.state = usageState{Version: stateVersion, Records: []Record{}}
		return nil
	}
	if st.Records == nil {
		st.Records = []Record{}
	}
	if st.Version == 0 {
		st.Version = stateVersion
	}
	s.state = st
	return nil
}
