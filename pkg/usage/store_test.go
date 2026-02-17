package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAppendAndQuery(t *testing.T) {
	tmp, err := os.MkdirTemp("", "usage-store-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmp)

	s := NewStore(tmp)
	err = s.Append(Record{
		Timestamp:        time.Now(),
		SessionKey:       "telegram:1",
		Provider:         "anthropic",
		Model:            "claude-sonnet-4-6",
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		UsageKnown:       true,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	recs := s.Query(Filter{SessionKey: "telegram:1"})
	if len(recs) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(recs))
	}
	if recs[0].TotalTokens != 15 {
		t.Fatalf("total_tokens = %d, want 15", recs[0].TotalTokens)
	}

	if _, err := os.Stat(filepath.Join(tmp, "state", "usage.json")); err != nil {
		t.Fatalf("usage.json missing: %v", err)
	}
}

func TestStorePrunesOldRecords(t *testing.T) {
	tmp, err := os.MkdirTemp("", "usage-prune-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmp)

	s := NewStore(tmp)
	old := time.Now().AddDate(0, 0, -31)
	recent := time.Now().AddDate(0, 0, -1)

	if err := s.Append(Record{Timestamp: old, SessionKey: "s1", UsageKnown: false}); err != nil {
		t.Fatalf("append old: %v", err)
	}
	if err := s.Append(Record{Timestamp: recent, SessionKey: "s1", UsageKnown: false}); err != nil {
		t.Fatalf("append recent: %v", err)
	}

	recs := s.Query(Filter{SessionKey: "s1"})
	if len(recs) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(recs))
	}
}

func TestAggregateRecordsKnownUnknown(t *testing.T) {
	records := []Record{
		{UsageKnown: true, PromptTokens: 100, CompletionTokens: 25, TotalTokens: 125},
		{UsageKnown: false},
		{UsageKnown: true, PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
	}
	agg := AggregateRecords(records)
	if agg.Calls != 3 || agg.KnownCalls != 2 || agg.UnknownCalls != 1 {
		t.Fatalf("unexpected counts: %+v", agg)
	}
	if agg.PromptTokens != 120 || agg.CompletionTokens != 30 || agg.TotalTokens != 150 {
		t.Fatalf("unexpected tokens: %+v", agg)
	}
}

func TestDayKeyUsesKolkata(t *testing.T) {
	tmp, err := os.MkdirTemp("", "usage-daykey-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmp)

	s := NewStore(tmp)
	ts := time.Date(2026, 2, 17, 18, 45, 0, 0, time.UTC) // 2026-02-18 in IST
	if got, want := s.DayKey(ts), "2026-02-18"; got != want {
		t.Fatalf("day key = %s, want %s", got, want)
	}
}
