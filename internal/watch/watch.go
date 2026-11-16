// Package watch owns WATCH/WAIT ideas. A triggered idea NEVER opens a position.
package watch

import (
	"errors"
	"time"
)

type Idea struct {
	Symbol        string    `json:"symbol"`
	Side          string    `json:"side"`
	SnapshotID    string    `json:"snapshot_id"`
	Status        string    `json:"status"`
	Reason        []string  `json:"reasons"`
	EntryLow      float64   `json:"entry_low"`
	EntryHigh     float64   `json:"entry_high"`
	InvalidateAt  float64   `json:"invalidate_at"`
	NextCheck     time.Time `json:"next_check"`
	Expires       time.Time `json:"expires"`
	OriginalPrice float64   `json:"original_price"`
}

func (i Idea) Validate() error {
	if i.Symbol == "" || (i.Side != "LONG" && i.Side != "SHORT") || i.SnapshotID == "" || i.Status != "WAIT" || i.EntryLow < 0 || i.EntryHigh < i.EntryLow || i.NextCheck.IsZero() || i.Expires.Before(i.NextCheck) {
		return errors.New("invalid wait idea")
	}
	return nil
}

// ShouldRecheck returns true ONLY for deep reevaluation, never for order placement.
func (i Idea) ShouldRecheck(now time.Time, price float64) (bool, string) {
	if now.After(i.Expires) {
		return false, "expired"
	}
	if now.Before(i.NextCheck) {
		return false, "cooldown"
	}
	if i.InvalidateAt > 0 && ((i.Side == "LONG" && price <= i.InvalidateAt) || (i.Side == "SHORT" && price >= i.InvalidateAt)) {
		return false, "invalidated"
	}
	if i.EntryLow > 0 && price >= i.EntryLow && price <= i.EntryHigh {
		return true, "entry_zone"
	}
	return true, "scheduled_recheck"
}
