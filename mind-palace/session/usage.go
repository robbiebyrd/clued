package session

import (
	"regexp"
	"sort"
	"strings"
)

// The groupings UsageQuery.GroupBy accepts.
const (
	UsageBySession = "session"
	UsageByModel   = "model"
	UsageByDay     = "day"
)

// UsageQuery totals the token usage the usage enricher recorded on the
// account's assistant transcript lines (enrichments.usage). Claude Code writes
// one line per content block of a message and repeats the message's usage on
// each, so the lines of one message (session id and message id) count once,
// with the largest value of each counter. GroupBy is UsageBySession (also
// the meaning of an empty GroupBy), UsageByModel or UsageByDay. Since and
// Until are inclusive days (YYYY-MM-DD, UTC) compared with the usage's day;
// empty SessionIDs, Since and Until mean no filter.
type UsageQuery struct {
	AccountID  string   `json:"-"`
	GroupBy    string   `json:"groupBy"`
	SessionIDs []string `json:"sessionIds"`
	Since      string   `json:"since"`
	Until      string   `json:"until"`
	// LongPromptTokens, when positive, splits each group and model by
	// whether a message's prompt (its context tokens) is over it.
	LongPromptTokens int64 `json:"longPromptTokens"`
}

// ValidUsageGroup reports whether g is a grouping UsageQuery accepts.
func ValidUsageGroup(g string) bool {
	return g == "" || g == UsageBySession || g == UsageByModel || g == UsageByDay
}

// UsageTotal is the usage of one group and model: Key is the session id, the
// model or the day the query grouped by (the model again when grouped by
// model). Messages counts distinct messages; PeakContextTokens is the largest
// input context (input plus cache tokens) of one of them.
type UsageTotal struct {
	Key                      string `json:"key"`
	Model                    string `json:"model"`
	Messages                 int64  `json:"messages"`
	InputTokens              int64  `json:"input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheCreation1hTokens    int64  `json:"cache_creation_1h_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	PeakContextTokens        int64  `json:"peak_context_tokens"`
	// LongPrompt marks the messages whose prompt is over the query's
	// LongPromptTokens.
	LongPrompt bool `json:"long_prompt"`
}

// Add folds the counters of o into t: sums, and the larger peak.
func (t *UsageTotal) Add(o UsageTotal) {
	t.Messages += o.Messages
	t.InputTokens += o.InputTokens
	t.CacheCreationInputTokens += o.CacheCreationInputTokens
	t.CacheCreation1hTokens += o.CacheCreation1hTokens
	t.CacheReadInputTokens += o.CacheReadInputTokens
	t.OutputTokens += o.OutputTokens
	t.PeakContextTokens = max(t.PeakContextTokens, o.PeakContextTokens)
}

// SortUsage orders totals by key, then model, short prompts first: the
// order both stores return.
func SortUsage(totals []UsageTotal) {
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].Key != totals[j].Key {
			return totals[i].Key < totals[j].Key
		}
		if totals[i].Model != totals[j].Model {
			return totals[i].Model < totals[j].Model
		}
		return !totals[i].LongPrompt && totals[j].LongPrompt
	})
}

// LongPromptTokens is the prompt size above which a model with a
// LongPrompt price charges it: Claude Haiku 5.5 prices a request whose
// prompt (input, cache writes and cache reads) is over 100,000 tokens higher.
const LongPromptTokens = 100_000

// ModelPrice is what a model costs in US dollars per million tokens. Cache
// writes cost 1.25 times the input price (2 times for the one-hour cache),
// as on the Anthropic API; cache reads cost CacheRead, or 0.1 times the
// input price when it is unset. LongPrompt, when set, is the price of a
// message whose prompt is over LongPromptTokens.
type ModelPrice struct {
	Input      float64     `json:"input"`
	Output     float64     `json:"output"`
	CacheRead  *float64    `json:"cacheRead,omitempty"`
	LongPrompt *ModelPrice `json:"longPrompt,omitempty"`
}

func usd(v float64) *float64 { return &v }

// DefaultModelPrices are the list prices on Anthropic's pricing page
// (platform.claude.com/docs/en/about-claude/pricing, read 2026-10-14) by
// model id. An entry matches that id and its dated or platform forms
// (claude-opus-4-1-20250805, claude-haiku-4-5@20251001, …), never a newer
// model whose id merely starts with it: those have no estimated cost until
// the config's modelPrices (Config.ModelPrices) names them. Batch, fast-mode
// and US-only (inference_geo) prices are not applied.
var DefaultModelPrices = map[string]ModelPrice{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: usd(0.25)},
	"claude-mythos-5-1": {Input: 10, Output: 50, CacheRead: usd(0.25)},
	"claude-fable-5":    {Input: 10, Output: 50},
	"claude-mythos-5":   {Input: 10, Output: 50},
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: usd(0.20)},
	"claude-opus-5":     {Input: 5, Output: 25},
	"claude-opus-4-8":   {Input: 5, Output: 25},
	"claude-opus-4-7":   {Input: 5, Output: 25},
	"claude-opus-4-6":   {Input: 5, Output: 25},
	"claude-opus-4-5":   {Input: 5, Output: 25},
	"claude-opus-4-1":   {Input: 15, Output: 75},
	"claude-opus-4":     {Input: 15, Output: 75},
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: usd(0.10)},
	"claude-sonnet-5":   {Input: 2, Output: 10},
	"claude-sonnet-4-6": {Input: 3, Output: 15},
	"claude-sonnet-4-5": {Input: 3, Output: 15},
	"claude-sonnet-4":   {Input: 3, Output: 15},
	"claude-haiku-5-5":  {Input: 0.10, Output: 0.50, LongPrompt: &ModelPrice{Input: 0.50, Output: 2.50}},
	"claude-haiku-4-5":  {Input: 1, Output: 5},
	"claude-3-5-haiku":  {Input: 0.80, Output: 4},
}

// snapshotSuffix is what may follow a built-in model id: a snapshot date
// (-20250805, @20251001) or a Bedrock version (-v1:0, :0).
var snapshotSuffix = regexp.MustCompile(`^(-\d{8}|@\d{8}|-v\d|:\d)`)

// PriceFor finds the price of model: the longest prefix of it named in
// overrides, else the built-in entry for its id (see DefaultModelPrices;
// a Bedrock "anthropic." or regional prefix is ignored). ok is false when
// neither names it.
func PriceFor(model string, overrides map[string]ModelPrice) (price ModelPrice, ok bool) {
	best := -1
	for prefix, p := range overrides {
		if strings.HasPrefix(model, prefix) && len(prefix) > best {
			best, price = len(prefix), p
		}
	}
	if best >= 0 {
		return price, true
	}
	id := model
	if i := strings.Index(id, "claude-"); i > 0 {
		id = id[i:]
	}
	for name, p := range DefaultModelPrices {
		rest, found := strings.CutPrefix(id, name)
		if found && (rest == "" || snapshotSuffix.MatchString(rest)) && len(name) > best {
			best, price = len(name), p
		}
	}
	return price, best >= 0
}

// Cost is the estimated price in US dollars of the tokens of t at p, at
// p.LongPrompt when t holds messages with long prompts and p has one.
func (t UsageTotal) Cost(p ModelPrice) float64 {
	if t.LongPrompt && p.LongPrompt != nil {
		p = *p.LongPrompt
	}
	cacheRead := 0.1 * p.Input
	if p.CacheRead != nil {
		cacheRead = *p.CacheRead
	}
	fiveMinute := max(t.CacheCreationInputTokens-t.CacheCreation1hTokens, 0)
	input := float64(t.InputTokens) + 1.25*float64(fiveMinute) + 2*float64(t.CacheCreation1hTokens)
	return (input*p.Input + float64(t.CacheReadInputTokens)*cacheRead + float64(t.OutputTokens)*p.Output) / 1e6
}
