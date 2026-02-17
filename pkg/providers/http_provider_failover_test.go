package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPProvider429IncludesHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.Header().Set("X-RateLimit-Requests-Reset", "1735689600")
		w.Header().Set("X-RateLimit-Tokens-Reset", "1735689700")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer ts.Close()

	p := NewHTTPProvider("k", ts.URL, "")
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "ping"}}, nil, "gpt-5-mini", map[string]interface{}{})
	if err == nil {
		t.Fatalf("expected error")
	}

	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitError, got %T", err)
	}
	if rl.RetryAfter != "120" {
		t.Fatalf("expected retry-after header")
	}
	if rl.RateLimitRequestsReset != "1735689600" {
		t.Fatalf("expected requests reset header")
	}
	if rl.Headers["Retry-After"] != "120" {
		t.Fatalf("expected headers map to contain Retry-After")
	}
}
