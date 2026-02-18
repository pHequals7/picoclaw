package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Record struct {
	Timestamp        time.Time `json:"timestamp"`
	DayKey           string    `json:"day_key"`
	SessionKey       string    `json:"session_key"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	UsageKnown       bool      `json:"usage_known"`
	Reason           string    `json:"reason"`
}

type Filter struct {
	SessionKey string
	DayKey     string
	Provider   string
	Limit      int
}

type Aggregate struct {
	Calls            int
	KnownCalls       int
	UnknownCalls     int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Store struct {
	mu      sync.RWMutex
	records []Record
	path    string
}

func NewStore(workspace string) *Store {
	s := &Store{
		records: make([]Record, 0, 256),
	}
	if workspace == "" {
		return s
	}
	_ = os.MkdirAll(workspace, 0755)
	s.path = filepath.Join(workspace, "usage.json")
	s.load()
	return s
}

func (s *Store) TodayKey() string {
	return time.Now().UTC().Format("2006-01-02")
}

func (s *Store) Add(r Record) {
	if r.DayKey == "" {
		r.DayKey = s.TodayKey()
	}
	if r.TotalTokens == 0 {
		r.TotalTokens = r.PromptTokens + r.CompletionTokens
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now().UTC()
	}

	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()

	s.save()
}

func (s *Store) LastBySession(sessionKey string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := len(s.records) - 1; i >= 0; i-- {
		if s.records[i].SessionKey == sessionKey {
			return s.records[i], true
		}
	}
	return Record{}, false
}

func (s *Store) Query(f Filter) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		if f.SessionKey != "" && r.SessionKey != f.SessionKey {
			continue
		}
		if f.DayKey != "" && r.DayKey != f.DayKey {
			continue
		}
		if f.Provider != "" && strings.ToLower(r.Provider) != strings.ToLower(f.Provider) {
			continue
		}
		out = append(out, r)
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[len(out)-f.Limit:]
	}
	return out
}

func AggregateRecords(records []Record) Aggregate {
	var agg Aggregate
	for _, r := range records {
		agg.Calls++
		if r.UsageKnown {
			agg.KnownCalls++
			agg.PromptTokens += r.PromptTokens
			agg.CompletionTokens += r.CompletionTokens
			agg.TotalTokens += r.TotalTokens
		} else {
			agg.UnknownCalls++
		}
	}
	return agg
}

func ProviderBreakdown(records []Record) map[string]Aggregate {
	out := map[string]Aggregate{}
	for _, r := range records {
		p := strings.TrimSpace(r.Provider)
		if p == "" {
			p = "unknown"
		}
		agg := out[p]
		agg.Calls++
		if r.UsageKnown {
			agg.KnownCalls++
			agg.PromptTokens += r.PromptTokens
			agg.CompletionTokens += r.CompletionTokens
			agg.TotalTokens += r.TotalTokens
		} else {
			agg.UnknownCalls++
		}
		out[p] = agg
	}
	return out
}

func (s *Store) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		return
	}
	s.records = records
}

func (s *Store) save() {
	if s.path == "" {
		return
	}
	s.mu.RLock()
	snapshot := make([]Record, len(s.records))
	copy(snapshot, s.records)
	s.mu.RUnlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0644)
}
