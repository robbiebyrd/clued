package session

import (
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

// SortUsage orders totals by key, then model, the order both stores return.
func SortUsage(totals []UsageTotal) {
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].Key != totals[j].Key {
			return totals[i].Key < totals[j].Key
		}
		return totals[i].Model < totals[j].Model
	})
}

// ModelPrice is what a model costs in US dollars per million tokens. Cache
// writes cost 1.25 times the input price (2 times for the one-hour cache) and
// cache reads 0.1 times, as on the Anthropic API.
type ModelPrice struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// DefaultModelPrices are the list prices of the models the table knows, by
// model id prefix. Models it does not know (newer ones among them) have no
// estimated cost until the config's modelPrices (Config.ModelPrices) names them.
var DefaultModelPrices = map[string]ModelPrice{
	"claude-opus-4-2025": {Input: 15, Output: 75},
	"claude-opus-4-1":    {Input: 15, Output: 75},
	"claude-opus-4-5":    {Input: 5, Output: 25},
	"claude-opus-4-6":    {Input: 5, Output: 25},
	"claude-sonnet-4":    {Input: 3, Output: 15},
	"claude-haiku-4-5":   {Input: 1, Output: 5},
	"claude-3-opus":      {Input: 15, Output: 75},
	"claude-3-7-sonnet":  {Input: 3, Output: 15},
	"claude-3-5-sonnet":  {Input: 3, Output: 15},
	"claude-3-5-haiku":   {Input: 0.8, Output: 4},
	"claude-3-haiku":     {Input: 0.25, Output: 1.25},
	"claude-3-sonnet":    {Input: 3, Output: 15},
}

// PriceFor finds the price of model: the longest prefix of it named in
// overrides, else in DefaultModelPrices. ok is false when neither names it.
func PriceFor(model string, overrides map[string]ModelPrice) (price ModelPrice, ok bool) {
	for _, table := range []map[string]ModelPrice{overrides, DefaultModelPrices} {
		best := -1
		for prefix, p := range table {
			if strings.HasPrefix(model, prefix) && len(prefix) > best {
				best, price = len(prefix), p
			}
		}
		if best >= 0 {
			return price, true
		}
	}
	return ModelPrice{}, false
}

// Cost is the estimated price in US dollars of the tokens of t at p.
func (t UsageTotal) Cost(p ModelPrice) float64 {
	fiveMinute := max(t.CacheCreationInputTokens-t.CacheCreation1hTokens, 0)
	inputEquivalent := float64(t.InputTokens) +
		1.25*float64(fiveMinute) +
		2*float64(t.CacheCreation1hTokens) +
		0.1*float64(t.CacheReadInputTokens)
	return (inputEquivalent*p.Input + float64(t.OutputTokens)*p.Output) / 1e6
}
