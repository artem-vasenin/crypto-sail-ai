// Package surface performs fast, explainable screening before the LLM.
package surface

import (
	"crypto-sail-ai/internal/market"
	"time"
)

type Status string

const (
	Candidate Status = "CANDIDATE"
	Watch     Status = "WATCH"
	Reject    Status = "REJECT"
)

type Result struct {
	SnapshotID   string        `json:"snapshot_id"`
	RunID        string        `json:"run_id,omitempty"`
	Symbol       string        `json:"symbol"`
	Status       Status        `json:"status"`
	Reasons      []string      `json:"reasons"`
	Ticker       market.Ticker `json:"ticker"`
	SpreadPct    float64       `json:"spread_pct"`
	AgeSeconds   float64       `json:"age_seconds"`
	HistoryClass string        `json:"history_class"`
}

type Settings struct {
	MinTurnover  float64
	MaxSpreadPct float64
	MaxAge       time.Duration
}

func Evaluate(t market.Ticker, i market.Instrument, now time.Time, c Settings) Result {
	r := Result{Symbol: t.Symbol, Status: Candidate, Ticker: t, HistoryClass: "unknown"}
	if !i.LaunchTime.IsZero() {
		switch age := now.Sub(i.LaunchTime); {
		case age < 0:
			r.HistoryClass = "invalid"
		case age < 7*24*time.Hour:
			r.HistoryClass = "new"
		case age < 30*24*time.Hour:
			r.HistoryClass = "limited"
		default:
			r.HistoryClass = "established"
		}
	}
	if err := t.Validate(); err != nil {
		r.Status = Reject
		r.Reasons = append(r.Reasons, "invalid_ticker")
		return r
	}
	if i.Symbol != t.Symbol || i.Status != "Trading" || i.ContractType != "LinearPerpetual" || i.QuoteCoin != "USDT" || i.SettleCoin != "USDT" {
		r.Status = Reject
		r.Reasons = append(r.Reasons, "invalid_instrument")
		return r
	}
	r.AgeSeconds = now.Sub(t.ObservedAt).Seconds()
	r.SpreadPct = t.SpreadPct()
	if !t.Fresh(now, c.MaxAge) {
		r.Status = Reject
		r.Reasons = append(r.Reasons, "stale_ticker")
	}
	if t.BidSize == 0 || t.AskSize == 0 {
		r.Reasons = append(r.Reasons, "empty_best_levels")
		if r.Status != Reject {
			r.Status = Watch
		}
	}
	if t.Turnover24h < c.MinTurnover {
		r.Reasons = append(r.Reasons, "low_turnover")
		if r.Status != Reject {
			r.Status = Watch
		}
	}
	if r.SpreadPct > c.MaxSpreadPct {
		r.Reasons = append(r.Reasons, "wide_spread")
		if r.Status != Reject {
			r.Status = Watch
		}
	}
	if r.HistoryClass == "new" || r.HistoryClass == "limited" {
		r.Reasons = append(r.Reasons, "limited_history")
	}
	return r
}
