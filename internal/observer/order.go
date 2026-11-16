package observer

import (
	"crypto-sail-ai/internal/market"
	"errors"
	"math"
	"time"
)

// EntryOrder is ONLY an offline maker-order simulator; it does not touch Bybit.
// Conservative fill assumption: a later aggressor trade must pass THROUGH
// the limit by >= one tick; mere OHLC/quote touch never implies execution.
// Participation is capped at 10% of observed aggressor trade quantity.
type EntryOrder struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"`
	Limit     float64   `json:"limit"`
	Tick      float64   `json:"tick"`
	Requested float64   `json:"requested"`
	Filled    float64   `json:"filled"`
	Status    string    `json:"status"`
	PlacedAt  time.Time `json:"placed_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Events    []Event   `json:"events"`
}
type TradePrint struct {
	At            time.Time
	Price         float64
	Qty           float64
	AggressorSide string
}

func NewPostOnlyOrder(id, symbol, side string, at time.Time, bid, ask, price, qty, tick float64, ttl time.Duration) (*EntryOrder, error) {
	if id == "" || !market.ValidSymbol(symbol) || at.IsZero() || (side != "LONG" && side != "SHORT") || ttl <= 0 || !market.Finite(price) || !market.Finite(qty) || !market.Finite(tick) || price <= 0 || qty <= 0 || tick <= 0 || bid <= 0 || ask <= 0 || bid > ask {
		return nil, errors.New("invalid virtual limit order")
	}
	if math.Abs(price/tick-math.Round(price/tick)) > 1e-6 {
		return nil, errors.New("price not aligned to tick")
	}
	if side == "LONG" && price > bid+tick*1e-7 || side == "SHORT" && price < ask-tick*1e-7 {
		return nil, errors.New("post-only order would cross spread")
	}
	return &EntryOrder{ID: id, Symbol: symbol, Side: side, Limit: price, Tick: tick, Requested: qty, Status: "PENDING", PlacedAt: at.UTC(), ExpiresAt: at.Add(ttl).UTC(), Events: []Event{{At: at.UTC(), Kind: "VIRTUAL_POSTONLY_PLACED", Price: price}}}, nil
}
func (o *EntryOrder) ObserveTrade(tr TradePrint) (float64, error) {
	if o.Status != "PENDING" && o.Status != "PARTIALLY_FILLED" {
		return 0, nil
	}
	if !tr.At.After(o.PlacedAt) {
		return 0, errors.New("cannot use a trade before/at placement")
	}
	if !market.Finite(tr.Price) || !market.Finite(tr.Qty) || tr.Price <= 0 || tr.Qty <= 0 {
		return 0, errors.New("bad trade print")
	}
	if !tr.At.Before(o.ExpiresAt) {
		o.Status = "EXPIRED"
		o.Events = append(o.Events, Event{At: tr.At.UTC(), Kind: "VIRTUAL_ORDER_EXPIRED"})
		return 0, nil
	}
	crossed := false
	if o.Side == "LONG" {
		crossed = tr.AggressorSide == "SELL" && tr.Price <= o.Limit-o.Tick*0.999999
	} else {
		crossed = tr.AggressorSide == "BUY" && tr.Price >= o.Limit+o.Tick*0.999999
	}
	if !crossed {
		return 0, nil
	}
	remaining := o.Requested - o.Filled
	take := math.Min(remaining, tr.Qty*0.1)
	if take <= 0 {
		return 0, nil
	}
	o.Filled += take
	status := "PARTIALLY_FILLED"
	if o.Requested-o.Filled <= math.Max(1e-10, o.Requested*1e-9) {
		o.Filled = o.Requested
		status = "FILLED"
	}
	o.Status = status
	o.Events = append(o.Events, Event{At: tr.At.UTC(), Kind: "VIRTUAL_MAKER_FILL", Price: o.Limit, Reason: "conservative trade-through, assumed 10pct participation"})
	return take, nil
}
func (o *EntryOrder) Cancel(at time.Time) error {
	if o.Status != "PENDING" && o.Status != "PARTIALLY_FILLED" {
		return errors.New("not cancellable")
	}
	if at.Before(o.PlacedAt) {
		return errors.New("cancel before placement")
	}
	o.Status = "CANCELLED"
	o.Events = append(o.Events, Event{At: at.UTC(), Kind: "VIRTUAL_ORDER_CANCELLED"})
	return nil
}
