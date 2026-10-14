package session

import (
	"math"
	"testing"
)

func TestPriceForPicksTheLongestPrefixOverridesFirst(t *testing.T) {
	cases := []struct {
		model     string
		overrides map[string]ModelPrice
		want      ModelPrice
		ok        bool
	}{
		{"claude-opus-4-20250514", nil, ModelPrice{15, 75}, true},
		{"claude-opus-4-1-20250805", nil, ModelPrice{15, 75}, true},
		{"claude-opus-4-6", nil, ModelPrice{5, 25}, true},
		{"claude-sonnet-4-5-20250929", nil, ModelPrice{3, 15}, true},
		{"claude-haiku-4-5", nil, ModelPrice{1, 5}, true},
		{"claude-unknown-9", nil, ModelPrice{}, false},
		{"claude-unknown-9", map[string]ModelPrice{"claude-unknown": {2, 4}}, ModelPrice{2, 4}, true},
		{"claude-sonnet-4-5", map[string]ModelPrice{"claude-sonnet-4-5": {9, 9}}, ModelPrice{9, 9}, true},
		{"claude-sonnet-4-5", map[string]ModelPrice{"claude": {1, 1}, "claude-sonnet": {2, 2}}, ModelPrice{2, 2}, true},
	}
	for _, c := range cases {
		got, ok := PriceFor(c.model, c.overrides)
		if got != c.want || ok != c.ok {
			t.Errorf("PriceFor(%q, %v) = %v, %v; want %v, %v", c.model, c.overrides, got, ok, c.want, c.ok)
		}
	}
}

func TestCostWeighsTheCacheTokens(t *testing.T) {
	total := UsageTotal{InputTokens: 1_000_000, CacheCreationInputTokens: 3_000_000, CacheCreation1hTokens: 1_000_000,
		CacheReadInputTokens: 10_000_000, OutputTokens: 1_000_000}
	// input 1M×$3 + 5-minute writes 2M×1.25×$3 + 1-hour writes 1M×2×$3 + reads 10M×0.1×$3 + output 1M×$15
	want := 3 + 7.5 + 6 + 3 + 15.0
	if got := total.Cost(ModelPrice{Input: 3, Output: 15}); math.Abs(got-want) > 1e-9 {
		t.Errorf("Cost = %v, want %v", got, want)
	}
}

func TestValidUsageGroup(t *testing.T) {
	for _, g := range []string{"", "session", "model", "day"} {
		if !ValidUsageGroup(g) {
			t.Errorf("%q refused", g)
		}
	}
	if ValidUsageGroup("week") {
		t.Error("week accepted")
	}
}
