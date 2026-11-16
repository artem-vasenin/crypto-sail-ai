// Package decisions applies independent, fail-closed gates to LLM suggestions.
package decisions

import (
	"crypto-sail-ai/internal/features"
	"crypto-sail-ai/internal/surface"
	"errors"
	"fmt"
)

type Decision string

const (
	Ready    Decision = "READY"
	Wait     Decision = "WAIT"
	Reject   Decision = "REJECT"
	NeedData Decision = "NEED_DATA"
)

type Proposal struct {
	SnapshotID   string   `json:"snapshot_id"`
	Symbol       string   `json:"symbol"`
	Side         string   `json:"side"`
	Decision     Decision `json:"decision"`
	Reasons      []string `json:"reason_codes"`
	Evidence     []string `json:"evidence"`
	Risks        []string `json:"risks"`
	EntryLow     float64  `json:"entry_low"`
	EntryHigh    float64  `json:"entry_high"`
	InvalidateAt float64  `json:"invalidate_at"`
	Explanation  string   `json:"explanation"`
}

func (p Proposal) Validate(expectedID, symbol string) error {
	if p.SnapshotID != expectedID || p.Symbol != symbol {
		return errors.New("snapshot or symbol mismatch")
	}
	if p.Side != "LONG" && p.Side != "SHORT" && p.Side != "NONE" {
		return errors.New("invalid side")
	}
	if p.Decision != Ready && p.Decision != Wait && p.Decision != Reject && p.Decision != NeedData {
		return errors.New("invalid decision")
	}
	if len(p.Reasons) == 0 || p.Explanation == "" || len(p.Explanation) > 2000 || len(p.Reasons) > 12 {
		return errors.New("empty/excessive reasons or explanation")
	}
	if p.Decision == Ready && p.Side == "NONE" {
		return errors.New("READY without side")
	}
	if p.EntryLow < 0 || p.EntryHigh < 0 || p.EntryHigh < p.EntryLow || p.InvalidateAt < 0 {
		return errors.New("invalid price zone")
	}
	return nil
}
func Gate(p Proposal, s surface.Result, tf map[string]features.Timeframe) (Decision, []string) {
	var reasons []string
	if s.Status == surface.Reject {
		reasons = append(reasons, "hard_surface_reject")
	}
	if s.Status == surface.Watch {
		reasons = append(reasons, "surface_watch")
	}
	if s.Symbol != p.Symbol {
		reasons = append(reasons, "symbol_mismatch")
	}
	if p.Decision == Ready {
		for _, k := range []string{"15", "60", "240"} {
			f, ok := tf[k]
			if !ok || f.Count < 50 || !f.ATR14.Valid || f.Structure.Regime == "UNKNOWN" {
				reasons = append(reasons, fmt.Sprintf("deep_insufficient_%s", k))
			}
		}
		if p.EntryLow <= 0 || p.EntryHigh <= 0 || p.InvalidateAt <= 0 {
			reasons = append(reasons, "invalid_entry_plan")
		}
		if p.Side == "LONG" && p.InvalidateAt >= p.EntryLow || p.Side == "SHORT" && p.InvalidateAt <= p.EntryHigh {
			reasons = append(reasons, "invalidation_wrong_side")
		}
	}
	if len(reasons) > 0 {
		if s.Status == surface.Reject {
			return Reject, reasons
		}
		return Wait, reasons
	}
	return p.Decision, p.Reasons
}
