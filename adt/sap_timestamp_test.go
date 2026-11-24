package adt

import (
	"testing"
	"time"
)

func TestParseSAPTimestamp(t *testing.T) {
	tests := []struct {
		name       string
		date, time string
		want       time.Time
	}{
		{"date and time", "20261104", "101112", time.Date(2026, 11, 4, 10, 11, 12, 0, time.UTC)},
		{"combined fourteen digits", "20261105123456", "", time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC)},
		{"midnight", "20261104", "000000", time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)},
		{"both empty", "", "", time.Time{}},
		{"initial date", "00000000", "000000", time.Time{}},
		{"initial combined", "00000000000000", "", time.Time{}},
		{"date without time", "20261104", "", time.Time{}},
		{"short date", "2026110", "101112", time.Time{}},
		{"short time", "20261104", "1011", time.Time{}},
		{"non-digit date", "2026-1-04", "101112", time.Time{}},
		{"non-digit time", "20261104", "10:11:", time.Time{}},
		{"month thirteen", "20261304", "101112", time.Time{}},
		{"day thirty-two", "20261132", "101112", time.Time{}},
		{"thirtieth of February", "20260230", "101112", time.Time{}},
		{"hour twenty-five", "20261104", "251112", time.Time{}},
		{"minute sixty", "20261104", "106012", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSAPTimestamp(tt.date, tt.time); !got.Equal(tt.want) {
				t.Errorf("parseSAPTimestamp(%q, %q) = %v, want %v", tt.date, tt.time, got, tt.want)
			}
		})
	}
}
