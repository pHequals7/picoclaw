package usage

import (
	"fmt"
	"strconv"
	"strings"
)

// HumanTokens formats token counts with K/M suffixes for quick scanning.
func HumanTokens(n int) string {
	if n >= 1_000_000 {
		return formatScaled(float64(n)/1_000_000, "M")
	}
	if n >= 1_000 {
		return formatScaled(float64(n)/1_000, "K")
	}
	return strconv.Itoa(n)
}

// GroupedInt formats integers with comma separators.
func GroupedInt(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}

	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if len(s) > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

func formatScaled(value float64, suffix string) string {
	s := fmt.Sprintf("%.1f", value)
	s = strings.TrimSuffix(s, ".0")
	return s + suffix
}
