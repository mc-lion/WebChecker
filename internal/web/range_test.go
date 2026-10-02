package web

import "testing"

func TestParseStatsRangeWhitelist(t *testing.T) {
	cases := []struct {
		in        string
		wantCode  string
		wantHours int
		wantStep  int
	}{
		{"1h", "1h", 1, 60},
		{"3h", "3h", 3, 3 * 60},
		{"6h", "6h", 6, 5 * 60},
		{"12h", "12h", 12, 10 * 60},
		{"1d", "1d", 24, 15 * 60},
		{"7d", "7d", 168, 3600},
		{"", "1d", 24, 15 * 60},
		{"2d", "1d", 24, 15 * 60},
		{"week", "1d", 24, 15 * 60},
		{"24h", "1d", 24, 15 * 60},
		{"1H", "1d", 24, 15 * 60},
	}
	for _, tc := range cases {
		got := parseStatsRange(tc.in)
		if got.Code != tc.wantCode || got.Hours != tc.wantHours || got.BucketSec != tc.wantStep {
			t.Fatalf("parseStatsRange(%q) = %+v, want code=%s hours=%d step=%d",
				tc.in, got, tc.wantCode, tc.wantHours, tc.wantStep)
		}
	}
}

func TestChartTimeLayout(t *testing.T) {
	if got := parseStatsRange("1h").chartTimeLayout(); got != "15:04" {
		t.Fatalf("1h layout %q", got)
	}
	if got := parseStatsRange("3h").chartTimeLayout(); got != "15:04" {
		t.Fatalf("3h layout %q", got)
	}
	if got := parseStatsRange("1d").chartTimeLayout(); got != "02.01 15:04" {
		t.Fatalf("1d layout %q", got)
	}
	if got := parseStatsRange("7d").chartTimeLayout(); got != "02.01 15:04" {
		t.Fatalf("7d layout %q", got)
	}
}

func TestStatsRangesCoverPlan(t *testing.T) {
	if len(statsRanges) != 6 {
		t.Fatalf("want 6 ranges, got %d", len(statsRanges))
	}
	seen := map[string]bool{}
	for _, r := range statsRanges {
		if r.Code == "" || r.Hours < 1 || r.BucketSec < 1 || r.Short == "" || r.Title == "" {
			t.Fatalf("incomplete range: %+v", r)
		}
		if seen[r.Code] {
			t.Fatalf("duplicate code %s", r.Code)
		}
		seen[r.Code] = true
	}
}
