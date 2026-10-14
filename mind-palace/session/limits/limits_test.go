package limits

import (
	"testing"
	"time"
)

const statusJSON = `{"session_id":"sess-1","cwd":"/work/x","transcript_path":"/t.jsonl","version":"2.1.0",
 "model":{"id":"claude-fable-5-1","display_name":"Fable 5.1"},
 "context_window":{"total_input_tokens":74454,"total_output_tokens":281,"context_window_size":1000000,"used_percentage":7.4,"remaining_percentage":92.6,"current_usage":{"input_tokens":2,"cache_creation_input_tokens":29104,"cache_read_input_tokens":45348}},
 "rate_limits":{"five_hour":{"used_percentage":12,"resets_at":1760400000},"seven_day":{"used_percentage":40.5,"resets_at":1760900000}},
 "cost":{"total_cost_usd":1.25,"total_duration_ms":5000},
 "workspace":{"current_dir":"/work/x"},"vim":{"mode":"NORMAL"}}`

var t0 = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func TestParseEventAndLine(t *testing.T) {
	in, err := Parse([]byte(statusJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	ev := in.Event(t0)
	if ev["hook_event_name"] != EventName || ev["session_id"] != "sess-1" || ev["cwd"] != "/work/x" || ev["version"] != "2.1.0" || ev["sampled_at"] != "2026-10-14T10:00:00Z" {
		t.Errorf("event = %v", ev)
	}
	if ev.Map("context_window")["used_percentage"] != 7.4 || ev.Map("rate_limits").Map("seven_day")["used_percentage"] != 40.5 || ev.Map("cost")["total_cost_usd"] != 1.25 || ev.Map("model")["id"] != "claude-fable-5-1" {
		t.Errorf("event payload = %v", ev)
	}
	if _, has := ev["workspace"]; has {
		t.Error("fields outside the sample must not be stored")
	}
	if got := in.Line(); got != "ctx 7% of 1M · 5h 12% · 7d 40% · Fable 5.1" {
		t.Errorf("line = %q", got)
	}

	// Without rate limits or a model name the line shrinks.
	bare, _ := Parse([]byte(`{"session_id":"s","context_window":{"used_percentage":37.2,"context_window_size":200000},"model":{"id":"claude-sonnet-4-5"}}`))
	if got := bare.Line(); got != "ctx 37% of 200k · claude-sonnet-4-5" {
		t.Errorf("bare line = %q", got)
	}
	if _, has := bare.Event(t0)["rate_limits"]; has {
		t.Error("an absent rate_limits map must not be stored")
	}
	empty, err := Parse([]byte("  \n"))
	if err != nil || empty.SessionID != "" || empty.Line() != "" {
		t.Errorf("empty input: %+v %v", empty, err)
	}
	if _, err := Parse([]byte("{")); err == nil {
		t.Error("invalid JSON must be an error")
	}
	for _, bad := range []string{`{}`, `{"session_id":"../x"}`, `{"session_id":"a/b"}`} {
		in, _ := Parse([]byte(bad))
		if in.Validate() == nil {
			t.Errorf("%s should not validate", bad)
		}
	}
}

func TestDue(t *testing.T) {
	in, _ := Parse([]byte(statusJSON))
	first := in.Sample(t0)
	if !Due(nil, first) {
		t.Error("the first sample is due")
	}
	same := func(d time.Duration) Sample { s := first; s.At = t0.Add(d); return s }
	if Due(&first, same(5*time.Second)) {
		t.Error("nothing changed within MinInterval")
	}
	if Due(&first, same(time.Minute)) {
		t.Error("nothing changed after MinInterval and before the heartbeat")
	}
	if !Due(&first, same(Heartbeat)) {
		t.Error("the heartbeat is due")
	}
	moved := same(time.Minute)
	moved.ContextUsed += 0.5
	if Due(&first, moved) {
		t.Error("half a point of context is not a step")
	}
	moved.ContextUsed = first.ContextUsed + ContextStep
	if !Due(&first, moved) {
		t.Error("a full point of context is due")
	}
	burst := moved
	burst.At = t0.Add(5 * time.Second)
	if Due(&first, burst) {
		t.Error("even a change waits for MinInterval")
	}
	limit := same(time.Minute)
	limit.SevenDayUsed = 41
	if !Due(&first, limit) {
		t.Error("a moved usage window is due")
	}
	reset := same(time.Minute)
	reset.FiveHourResets = 1760418000
	if !Due(&first, reset) {
		t.Error("a new reset time is due")
	}
	// A sample that gains or loses the rate limits counts as a change.
	noLimits, _ := Parse([]byte(`{"session_id":"sess-1","context_window":{"used_percentage":7.4}}`))
	if !Due(&first, noLimits.Sample(t0.Add(time.Minute))) {
		t.Error("losing the usage windows is a change")
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if LoadState(dir, "sess-1") != nil {
		t.Error("no state yet")
	}
	in, _ := Parse([]byte(statusJSON))
	s := in.Sample(t0)
	if err := SaveState(dir, "weird/../id", s); err != nil {
		t.Fatal(err)
	}
	got := LoadState(dir, "weird/../id")
	if got == nil || !got.At.Equal(t0) || got.ContextUsed != 7.4 || got.SevenDayResets != 1760900000 {
		t.Errorf("state = %+v", got)
	}
	if LoadState(dir, "other") != nil {
		t.Error("state is per session")
	}
}
