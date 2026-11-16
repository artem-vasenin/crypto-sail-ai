// Package observer models filled VIRTUAL positions only, with conservative bar stops.
// There is deliberately no exchange-order API, credential loader or live switch.
package observer

import (
	"crypto-sail-ai/internal/market"
	"errors"
	"fmt"
	"math"
	"time"
)

type Rules struct {
	EntryFeeRate    float64 `json:"entry_fee_rate"` // fraction, not percent
	ExitFeeRate     float64 `json:"exit_fee_rate"`
	SlippageRate    float64 `json:"slippage_rate"`
	FundingCostUSDT float64 `json:"funding_cost_usdt"` // non-negative assumed charge
	BETriggerR      float64 `json:"be_trigger_r"`
	TrailTriggerR   float64 `json:"trail_trigger_r"`
	ATRMultiplier   float64 `json:"atr_multiplier"`
	TickSize        float64 `json:"tick_size"`
	MinNetTargetR   float64 `json:"min_net_target_r"`
}

func DefaultRules() Rules {
	return Rules{EntryFeeRate: 0.0002, ExitFeeRate: 0.00055, SlippageRate: 0.0002, BETriggerR: 1.0, TrailTriggerR: 1.5, ATRMultiplier: 2.5, TickSize: 0.01, MinNetTargetR: 1.0}
}
func (r Rules) Validate() error {
	for _, x := range []float64{r.EntryFeeRate, r.ExitFeeRate, r.SlippageRate, r.FundingCostUSDT, r.BETriggerR, r.TrailTriggerR, r.ATRMultiplier, r.TickSize, r.MinNetTargetR} {
		if !market.Finite(x) || x < 0 {
			return errors.New("invalid observer rule")
		}
	}
	if r.EntryFeeRate >= .1 || r.ExitFeeRate >= .1 || r.SlippageRate >= .1 || r.TickSize <= 0 || r.BETriggerR <= 0 || r.TrailTriggerR < r.BETriggerR || r.ATRMultiplier <= 0 || r.MinNetTargetR < 0 {
		return errors.New("out-of-range rules")
	}
	return nil
}

type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	OldSL  float64   `json:"old_sl,omitempty"`
	NewSL  float64   `json:"new_sl,omitempty"`
	Price  float64   `json:"price,omitempty"`
	Reason string    `json:"reason,omitempty"`
}
type Position struct {
	ID               string    `json:"id"`
	SourceSnapshotID string    `json:"source_snapshot_id,omitempty"`
	Symbol           string    `json:"symbol"`
	Side             string    `json:"side"`
	EntryTime        time.Time `json:"entry_time"`
	Entry            float64   `json:"entry"`
	Qty              float64   `json:"quantity"`
	InitialSL        float64   `json:"initial_sl"`
	CurrentSL        float64   `json:"current_sl"`
	TP               float64   `json:"take_profit"`
	InitialRiskPrice float64   `json:"initial_risk_price"`
	InitialRiskUSDT  float64   `json:"initial_risk_usdt"`
	Status           string    `json:"status"`
	Rules            Rules     `json:"rules"`
	MFE              float64   `json:"mfe_price"`
	MAE              float64   `json:"mae_price"`
	MaxFavorableR    float64   `json:"max_favorable_r"`
	ExitTime         time.Time `json:"exit_time,omitempty"`
	ExitPrice        float64   `json:"exit_price,omitempty"`
	ExitReason       string    `json:"exit_reason,omitempty"`
	GrossPnL         float64   `json:"gross_pnl"`
	NetPnL           float64   `json:"net_pnl"`
	NetR             float64   `json:"net_r"`
	Events           []Event   `json:"events"`
	LastBarTime      time.Time `json:"last_bar_time,omitempty"`
	BEApplied        bool      `json:"be_applied"`
}

func PositionSize(riskUSDT, entry, stop float64) (float64, error) {
	if riskUSDT <= 0 || entry <= 0 || stop <= 0 || entry == stop {
		return 0, errors.New("bad sizing inputs")
	}
	return riskUSDT / math.Abs(entry-stop), nil
}

func NewFilled(id, symbol, side string, at time.Time, entry, stop, tp, qty float64, r Rules) (*Position, error) {
	if id == "" || !market.ValidSymbol(symbol) || (side != "LONG" && side != "SHORT") || at.IsZero() {
		return nil, errors.New("invalid identity")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	for _, x := range []float64{entry, stop, tp, qty} {
		if !market.Finite(x) || x <= 0 {
			return nil, errors.New("invalid price/size")
		}
	}
	if side == "LONG" && !(stop < entry && entry < tp) || side == "SHORT" && !(tp < entry && entry < stop) {
		return nil, errors.New("SL/TP on wrong side")
	}
	risk := math.Abs(entry-stop) * qty
	p := &Position{ID: id, Symbol: symbol, Side: side, EntryTime: at.UTC(), Entry: entry, InitialSL: stop, CurrentSL: stop, TP: tp, Qty: qty, Rules: r, InitialRiskPrice: math.Abs(entry - stop), InitialRiskUSDT: risk, Status: "OPEN"}
	hypothetical := p.pnl(tp, false)
	if hypothetical/risk < r.MinNetTargetR {
		return nil, fmt.Errorf("net TP too small: %.3fR", hypothetical/risk)
	}
	p.Events = append(p.Events, Event{At: at.UTC(), Kind: "VIRTUAL_FILL_ASSUMED", Price: entry, Reason: "fill price is an explicit replay assumption; not an exchange execution"})
	return p, nil
}
func (p *Position) favorable(price float64) float64 {
	if p.Side == "LONG" {
		return price - p.Entry
	}
	return p.Entry - price
}
func (p *Position) pnl(exit float64, isStop bool) float64 {
	// Conservatively assume slippage on every exit including TP and stop.
	effective := exit * (1 - p.Rules.SlippageRate)
	if p.Side == "SHORT" {
		effective = exit * (1 + p.Rules.SlippageRate)
	}
	gross := p.favorable(effective) * p.Qty
	fees := p.Entry*p.Qty*p.Rules.EntryFeeRate + effective*p.Qty*p.Rules.ExitFeeRate
	_ = isStop
	return gross - fees - p.Rules.FundingCostUSDT
}
func (p *Position) BreakevenStop() float64 {
	r := p.Rules
	entryCost := p.Entry*r.EntryFeeRate + r.FundingCostUSDT/p.Qty
	var x float64
	if p.Side == "LONG" {
		x = (p.Entry + entryCost) / ((1 - r.SlippageRate) * (1 - r.ExitFeeRate))
		return math.Ceil((x-1e-10)/r.TickSize) * r.TickSize
	}
	x = (p.Entry - entryCost) / ((1 + r.SlippageRate) * (1 + r.ExitFeeRate))
	return math.Floor((x+1e-10)/r.TickSize) * r.TickSize
}
func (p *Position) ImproveStop(at time.Time, newSL, currentPrice float64, reason string) bool {
	if p.Status != "OPEN" || !market.Finite(newSL) || !market.Finite(currentPrice) || currentPrice <= 0 {
		return false
	}
	minStep := p.Rules.TickSize * .999999
	if p.Side == "LONG" {
		if newSL < p.CurrentSL+minStep || newSL >= currentPrice {
			return false
		}
	} else {
		if newSL > p.CurrentSL-minStep || newSL <= currentPrice {
			return false
		}
	}
	old := p.CurrentSL
	p.CurrentSL = newSL
	p.Events = append(p.Events, Event{At: at.UTC(), Kind: "STOP_IMPROVED", OldSL: old, NewSL: newSL, Reason: reason})
	return true
}

// OnClosedBar handles SL before TP on ambiguous OHLC bars, then moves stops at bar close.
// Stops updated at close become active only in the NEXT bar.
func (p *Position) OnClosedBar(c market.Candle, atr float64) error {
	if p.Status != "OPEN" {
		return errors.New("position already closed")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if !c.OpenTime.After(p.EntryTime) || !p.LastBarTime.IsZero() && !c.OpenTime.After(p.LastBarTime) {
		return errors.New("non-monotonic/reused or entry bar")
	}
	if !market.Finite(atr) || atr < 0 {
		return errors.New("invalid ATR")
	}
	p.LastBarTime = c.OpenTime
	if p.Side == "LONG" {
		p.MFE = math.Max(p.MFE, c.High-p.Entry)
		p.MAE = math.Max(p.MAE, p.Entry-c.Low)
	} else {
		p.MFE = math.Max(p.MFE, p.Entry-c.Low)
		p.MAE = math.Max(p.MAE, c.High-p.Entry)
	}
	p.MaxFavorableR = p.MFE / p.InitialRiskPrice
	stopHit, tpHit := false, false
	if p.Side == "LONG" {
		stopHit = c.Low <= p.CurrentSL
		tpHit = c.High >= p.TP
	} else {
		stopHit = c.High >= p.CurrentSL
		tpHit = c.Low <= p.TP
	}
	if stopHit {
		exit := p.CurrentSL
		if p.Side == "LONG" && c.Open < exit {
			exit = c.Open
		}
		if p.Side == "SHORT" && c.Open > exit {
			exit = c.Open
		}
		p.close(c.OpenTime, exit, "SL")
		if tpHit {
			p.Events = append(p.Events, Event{At: c.OpenTime, Kind: "AMBIGUOUS_BAR", Reason: "SL and TP touched; SL assumed first"})
		}
		return nil
	}
	if tpHit {
		p.close(c.OpenTime, p.TP, "TP")
		return nil
	}
	// Do not tighten stops if bar CLOSE is not sufficiently profitable.
	gainR := p.favorable(c.Close) / p.InitialRiskPrice
	if gainR >= p.Rules.BETriggerR && !p.BEApplied {
		if p.ImproveStop(c.OpenTime, p.BreakevenStop(), c.Close, "COST_AWARE_BREAKEVEN") {
			p.BEApplied = true
		}
	}
	if gainR >= p.Rules.TrailTriggerR && atr > 0 {
		var candidate float64
		if p.Side == "LONG" {
			candidate = math.Floor((c.Close-p.Rules.ATRMultiplier*atr)/p.Rules.TickSize) * p.Rules.TickSize
		} else {
			candidate = math.Ceil((c.Close+p.Rules.ATRMultiplier*atr)/p.Rules.TickSize) * p.Rules.TickSize
		}
		p.ImproveStop(c.OpenTime, candidate, c.Close, "ATR_TRAILING")
	}
	return nil
}
func (p *Position) close(at time.Time, price float64, reason string) {
	p.Status = "CLOSED"
	p.ExitTime = at.UTC()
	p.ExitPrice = price
	p.ExitReason = reason
	p.GrossPnL = p.favorable(price) * p.Qty
	p.NetPnL = p.pnl(price, reason == "SL")
	p.NetR = p.NetPnL / p.InitialRiskUSDT
	p.Events = append(p.Events, Event{At: at.UTC(), Kind: "VIRTUAL_CLOSE", Price: price, Reason: reason})
}

// PostExit records checkpoints as actual new observations arrive; no invented future prices.
type Checkpoint struct {
	Minutes      int       `json:"minutes"`
	At           time.Time `json:"at"`
	Price        float64   `json:"price"`
	FromExitPct  float64   `json:"from_exit_pct"`
	FromEntryPct float64   `json:"from_entry_pct"`
}

func PostExit(p *Position, seen map[int]float64) ([]Checkpoint, error) {
	if p.Status != "CLOSED" {
		return nil, errors.New("position must be closed")
	}
	var out []Checkpoint
	for _, mins := range []int{5, 15, 30, 60, 120, 180} {
		price, ok := seen[mins]
		if !ok {
			continue
		}
		if !market.Finite(price) || price <= 0 {
			return nil, errors.New("invalid post-exit price")
		}
		out = append(out, Checkpoint{Minutes: mins, At: p.ExitTime.Add(time.Duration(mins) * time.Minute), Price: price, FromExitPct: (price/p.ExitPrice - 1) * 100, FromEntryPct: (price/p.Entry - 1) * 100})
	}
	return out, nil
}
