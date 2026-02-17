package usage

import "testing"

func TestHumanTokens(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{999, "999"},
		{1000, "1K"},
		{1532, "1.5K"},
		{10_000, "10K"},
		{999_999, "1000K"},
		{1_000_000, "1M"},
		{1_550_000, "1.6M"},
	}

	for _, tc := range tests {
		if got := HumanTokens(tc.in); got != tc.want {
			t.Fatalf("HumanTokens(%d)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestGroupedInt(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{12, "12"},
		{999, "999"},
		{1000, "1,000"},
		{12_345, "12,345"},
		{1_000_000, "1,000,000"},
	}

	for _, tc := range tests {
		if got := GroupedInt(tc.in); got != tc.want {
			t.Fatalf("GroupedInt(%d)=%q want %q", tc.in, got, tc.want)
		}
	}
}
