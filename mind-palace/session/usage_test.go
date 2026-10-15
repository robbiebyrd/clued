package session

import (
	"math"
	"testing"
)

func TestPriceForMatchesModelIDsAndTheirSnapshots(t *testing.T) {
	cases := []struct {
		model     string
		overrides map[string]ModelPrice
		input     float64
		output    float64
		ok        bool
	}{
		{"claude-fable-5-1", nil, 10, 50, true},
		{"claude-opus-5-5", nil, 4, 20, true},
		{"claude-opus-5", nil, 5, 25, true},
		{"claude-sonnet-5-5", nil, 2, 10, true},
		{"claude-sonnet-4-6", nil, 3, 15, true},
		{"claude-haiku-5-5", nil, 0.10, 0.50, true},
		{"claude-opus-4-20250514", nil, 15, 75, true},
		{"claude-opus-4-1-20250805", nil, 15, 75, true},
		{"claude-sonnet-4-5-20250929", nil, 3, 15, true},
		{"claude-haiku-4-5@20251001", nil, 1, 5, true},
		{"anthropic.claude-sonnet-4-5-20250929-v1:0", nil, 3, 15, true},
		{"us.anthropic.claude-opus-4-1-20250805-v1:0", nil, 15, 75, true},
		{"claude-3-5-haiku-20241022", nil, 0.80, 4, true},
		// Bedrock: regional and global inference profiles, ARNs, versions.
		{"global.anthropic.claude-sonnet-4-5-20250929-v1:0", nil, 3, 15, true},
		{"eu.anthropic.claude-haiku-4-5-20251001-v1:0", nil, 1, 5, true},
		{"apac.anthropic.claude-sonnet-4-20250514-v1:0", nil, 3, 15, true},
		{"arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-opus-4-1-20250805-v1:0", nil, 15, 75, true},
		{"anthropic.claude-3-5-haiku-20241022-v1:0", nil, 0.80, 4, true},
		{"anthropic.claude-opus-4-1-20250805-v2", nil, 15, 75, true},
		// Vertex AI: @snapshot ids and publisher paths.
		{"claude-opus-4-1@20250805", nil, 15, 75, true},
		{"claude-sonnet-4-5@20250929", nil, 3, 15, true},
		{"publishers/anthropic/models/claude-sonnet-4@20250514", nil, 3, 15, true},
		{"Claude-Sonnet-4-5[1m]", nil, 3, 15, true},
		{"us.anthropic.claude-opus-5-6-20261001-v1:0", nil, 0, 0, false}, // a newer model, on Bedrock too
		{"us.anthropic.claude-unknown-9-v1:0", map[string]ModelPrice{"claude-unknown-9": {Input: 2, Output: 4}}, 2, 4, true},
		{"claude-opus-5-6", nil, 0, 0, false},   // a newer model is not priced as Opus 5
		{"claude-sonnet-4-7", nil, 0, 0, false}, // nor as Sonnet 4
		{"claude-opus-4-5x", nil, 0, 0, false},  // a suffix that is no snapshot
		{"claude-unknown-9", nil, 0, 0, false},
		{"claude-unknown-9", map[string]ModelPrice{"claude-unknown": {Input: 2, Output: 4}}, 2, 4, true},
		{"claude-sonnet-4-5", map[string]ModelPrice{"claude-sonnet-4-5": {Input: 9, Output: 9}}, 9, 9, true},
		{"claude-sonnet-4-5", map[string]ModelPrice{"claude": {Input: 1, Output: 1}, "claude-sonnet": {Input: 2, Output: 2}}, 2, 2, true},
	}
	for _, c := range cases {
		got, ok := PriceFor(c.model, c.overrides)
		if got.Input != c.input || got.Output != c.output || ok != c.ok {
			t.Errorf("PriceFor(%q, %v) = %v/%v, %v; want %v/%v, %v", c.model, c.overrides, got.Input, got.Output, ok, c.input, c.output, c.ok)
		}
	}
}

func TestNormalizeModelID(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-4-1":                                      "claude-opus-4-1",
		"claude-opus-4-1-20250805":                             "claude-opus-4-1",
		"us.anthropic.claude-opus-4-1-20250805-v1:0":           "claude-opus-4-1",
		"global.anthropic.claude-sonnet-4-5-20250929-v1:0":     "claude-sonnet-4-5",
		"claude-haiku-4-5@20251001":                            "claude-haiku-4-5",
		"publishers/anthropic/models/claude-sonnet-4@20250514": "claude-sonnet-4",
		"claude-sonnet-4-5[1m]":                                "claude-sonnet-4-5",
		"claude-opus-4-5x":                                     "claude-opus-4-5x",
		" GPT-5 ":                                              "gpt-5",
	} {
		if got := NormalizeModelID(in); got != want {
			t.Errorf("NormalizeModelID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCostWeighsTheCacheTokens(t *testing.T) {
	total := UsageTotal{InputTokens: 1_000_000, CacheCreationInputTokens: 3_000_000, CacheCreation1hTokens: 1_000_000,
		CacheReadInputTokens: 10_000_000, OutputTokens: 1_000_000}
	cases := []struct {
		model string
		want  float64
	}{
		// input 1M×$3 + 5-minute writes 2M×1.25×$3 + 1-hour writes 1M×2×$3 + reads 10M×0.1×$3 + output 1M×$15
		{"claude-sonnet-4-5", 3 + 7.5 + 6 + 3 + 15},
		// Opus 5.5 reads at $0.20 (0.05×): 4 + 2M×1.25×4 + 1M×2×4 + 10M×0.20 + 20
		{"claude-opus-5-5", 4 + 10 + 8 + 2 + 20},
		// Fable 5.1 reads at $0.25 (0.025×): 10 + 25 + 20 + 2.5 + 50
		{"claude-fable-5-1", 10 + 25 + 20 + 2.5 + 50},
	}
	for _, c := range cases {
		p, _ := PriceFor(c.model, nil)
		if got := total.Cost(p); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: Cost = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestCostOfLongPrompts(t *testing.T) {
	haiku, _ := PriceFor("claude-haiku-5-5", nil)
	short := UsageTotal{InputTokens: 1_000_000, CacheReadInputTokens: 1_000_000, OutputTokens: 1_000_000}
	long := short
	long.LongPrompt = true
	if got, want := short.Cost(haiku), 0.10+0.01+0.50; math.Abs(got-want) > 1e-9 {
		t.Errorf("short prompts = %v, want %v", got, want)
	}
	if got, want := long.Cost(haiku), 0.50+0.05+2.50; math.Abs(got-want) > 1e-9 {
		t.Errorf("long prompts = %v, want %v", got, want)
	}
	sonnet, _ := PriceFor("claude-sonnet-5-5", nil)
	if long.Cost(sonnet) != short.Cost(sonnet) {
		t.Error("a model without a long-prompt price charged more for long prompts")
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
