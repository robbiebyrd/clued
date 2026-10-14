package query

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const defaultUsageLimit = 50

// UsageArgs are the usage tool parameters. GroupBy is session (the
// default), model or day; Since and Until are inclusive UTC days
// (YYYY-MM-DD).
type UsageArgs struct {
	GroupBy     string `json:"group_by,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Since       string `json:"since,omitempty"`
	Until       string `json:"until,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// UsageCounts are the token counters of a group or a model and its estimated
// cost in US dollars; CostUSD is null when none of its models has a price,
// and covers only the priced ones otherwise.
type UsageCounts struct {
	Messages                 int64    `json:"messages"`
	InputTokens              int64    `json:"input_tokens"`
	CacheCreationInputTokens int64    `json:"cache_creation_input_tokens"`
	CacheCreation1hTokens    int64    `json:"cache_creation_1h_tokens"`
	CacheReadInputTokens     int64    `json:"cache_read_input_tokens"`
	OutputTokens             int64    `json:"output_tokens"`
	PeakContextTokens        int64    `json:"peak_context_tokens"`
	CostUSD                  *float64 `json:"cost_usd"`
}

// UsageModel is the share of one model in a group.
type UsageModel struct {
	Model string `json:"model"`
	UsageCounts
}

// UsageGroup is one session, model or day. Models breaks it down by model
// (omitted when grouped by model).
type UsageGroup struct {
	Key string `json:"key"`
	UsageCounts
	Models []UsageModel `json:"models,omitempty"`
}

// UsageReport is the usage tool's answer: the groups (at most Limit of them,
// the costliest first, or in day order when grouped by day), the total over
// every group, and the models the price table does not know.
type UsageReport struct {
	GroupBy        string       `json:"group_by"`
	Groups         []UsageGroup `json:"groups"`
	Total          UsageCounts  `json:"total"`
	UnpricedModels []string     `json:"unpriced_models"`
}

// Usage totals the tokens of the scope's assistant messages, each message
// once, and estimates their cost from s.Prices over the built-in price
// table, pricing messages whose prompt is over session.LongPromptTokens at
// the model's LongPrompt price when it has one. Lines count once the usage
// enricher has recorded them.
func (s *Service) Usage(ctx context.Context, args UsageArgs) (UsageReport, error) {
	if !session.ValidUsageGroup(args.GroupBy) {
		return UsageReport{}, fmt.Errorf("group_by %q is not session, model or day", args.GroupBy)
	}
	group := args.GroupBy
	if group == "" {
		group = session.UsageBySession
	}
	for _, day := range []string{args.Since, args.Until} {
		if _, err := time.Parse(time.DateOnly, day); day != "" && err != nil {
			return UsageReport{}, fmt.Errorf("%q is not a day (YYYY-MM-DD)", day)
		}
	}
	report := UsageReport{GroupBy: group, Groups: []UsageGroup{}, UnpricedModels: []string{}}
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil || (scoped && len(sessionIDs) == 0) {
		return report, err
	}
	totals, err := s.Store.UsageTotals(ctx, session.UsageQuery{
		AccountID: s.AccountID, GroupBy: group, SessionIDs: sessionIDs, Since: args.Since, Until: args.Until,
		LongPromptTokens: session.LongPromptTokens,
	})
	if err != nil {
		return UsageReport{}, err
	}
	var all session.UsageTotal
	var allCost costSum
	unpriced := map[string]bool{}
	byKey := map[string]int{}
	for _, t := range totals {
		cost, priced := s.cost(t)
		if !priced {
			unpriced[t.Model] = true
		}
		i, seen := byKey[t.Key]
		if !seen {
			i = len(report.Groups)
			byKey[t.Key] = i
			report.Groups = append(report.Groups, UsageGroup{Key: t.Key})
		}
		g := &report.Groups[i]
		g.UsageCounts = g.UsageCounts.plus(t, cost, priced)
		if group != session.UsageByModel {
			// A model's short and long prompts arrive as two totals.
			if n := len(g.Models); n > 0 && g.Models[n-1].Model == t.Model {
				g.Models[n-1].UsageCounts = g.Models[n-1].UsageCounts.plus(t, cost, priced)
			} else {
				g.Models = append(g.Models, UsageModel{Model: t.Model, UsageCounts: UsageCounts{}.plus(t, cost, priced)})
			}
		}
		all.Add(t)
		allCost.add(cost, priced)
	}
	report.Total = counts(all, allCost.value())
	for m := range unpriced {
		report.UnpricedModels = append(report.UnpricedModels, m)
	}
	sort.Strings(report.UnpricedModels)
	if group != session.UsageByDay {
		sort.SliceStable(report.Groups, func(i, j int) bool {
			ci, cj := costOrZero(report.Groups[i].CostUSD), costOrZero(report.Groups[j].CostUSD)
			if ci != cj {
				return ci > cj
			}
			return tokens(report.Groups[i].UsageCounts) > tokens(report.Groups[j].UsageCounts)
		})
	}
	if limit := withDefault(args.Limit, defaultUsageLimit); len(report.Groups) > limit {
		report.Groups = report.Groups[:limit]
	}
	return report, nil
}

// cost prices t by its model; priced is false when no price names it.
func (s *Service) cost(t session.UsageTotal) (cost float64, priced bool) {
	p, ok := session.PriceFor(t.Model, s.Prices)
	if !ok {
		return 0, false
	}
	return t.Cost(p), true
}

// plus adds t to c, with t's cost when it is priced.
func (c UsageCounts) plus(t session.UsageTotal, cost float64, priced bool) UsageCounts {
	sum := session.UsageTotal{
		Messages: c.Messages, InputTokens: c.InputTokens, CacheCreationInputTokens: c.CacheCreationInputTokens,
		CacheCreation1hTokens: c.CacheCreation1hTokens, CacheReadInputTokens: c.CacheReadInputTokens,
		OutputTokens: c.OutputTokens, PeakContextTokens: c.PeakContextTokens,
	}
	sum.Add(t)
	var total costSum
	if c.CostUSD != nil {
		total.add(*c.CostUSD, true)
	}
	total.add(cost, priced)
	return counts(sum, total.value())
}

func counts(t session.UsageTotal, cost *float64) UsageCounts {
	return UsageCounts{
		Messages: t.Messages, InputTokens: t.InputTokens, CacheCreationInputTokens: t.CacheCreationInputTokens,
		CacheCreation1hTokens: t.CacheCreation1hTokens, CacheReadInputTokens: t.CacheReadInputTokens,
		OutputTokens: t.OutputTokens, PeakContextTokens: t.PeakContextTokens, CostUSD: cost,
	}
}

// costSum adds the costs of priced totals; its value is nil when none was.
type costSum struct {
	usd    float64
	priced bool
}

func (c *costSum) add(usd float64, priced bool) {
	if priced {
		c.usd += usd
		c.priced = true
	}
}

func (c costSum) value() *float64 {
	if !c.priced {
		return nil
	}
	v := math.Round(c.usd*1e4) / 1e4
	return &v
}

func costOrZero(c *float64) float64 {
	if c == nil {
		return 0
	}
	return *c
}

func tokens(c UsageCounts) int64 {
	return c.InputTokens + c.CacheCreationInputTokens + c.CacheReadInputTokens + c.OutputTokens
}
