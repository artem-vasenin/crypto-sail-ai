package market

import (
	"math"
	"testing"
	"time"
)

func candle(at time.Time) Candle {
	return Candle{OpenTime: at.UTC(), Open: 100, High: 102, Low: 98, Close: 101, Volume: 10, Turnover: 1000}
}
func TestValidateCandles(t *testing.T) {
	now := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	cs := []Candle{candle(now.Add(-2 * time.Hour)), candle(now.Add(-time.Hour))}
	if e := ValidateCandles(cs, "60", now, true); e != nil {
		t.Fatal(e)
	}
	for _, tt := range []struct {
		name   string
		edit   func([]Candle)
		strict bool
	}{
		{"duplicate", func(a []Candle) { a[1].OpenTime = a[0].OpenTime }, false},
		{"future", func(a []Candle) { a[1].OpenTime = now }, false},
		{"gap", func(a []Candle) { a[1].OpenTime = now.Add(-3 * time.Hour) }, true},
		{"invalid_ohlc", func(a []Candle) { a[0].High = 90 }, false},
		{"NaN", func(a []Candle) { a[0].Low = math.NaN() }, false},
		{"unaligned", func(a []Candle) { a[0].OpenTime = a[0].OpenTime.Add(time.Minute) }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bad := append([]Candle(nil), cs...)
			tt.edit(bad)
			if err := ValidateCandles(bad, "60", now, tt.strict); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	// Bybit weekly candle opens Monday; the Unix epoch starts Thursday.
	monday := time.Date(2025, 12, 22, 0, 0, 0, 0, time.UTC)
	if e := ValidateCandles([]Candle{candle(monday), candle(monday.Add(7 * 24 * time.Hour))}, "W", time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC), true); e != nil {
		t.Fatal(e)
	}
}
func TestTickerAndInterval(t *testing.T) {
	now := time.Now().UTC()
	v := Ticker{Symbol: "ABCUSDT", Last: 0.00001, Bid: .000009, Ask: .000011, BidSize: 1, AskSize: 2, ObservedAt: now}
	if e := v.Validate(); e != nil {
		t.Fatal(e)
	}
	if v.SpreadPct() <= 0 {
		t.Fatal("spread")
	}
	if !v.Fresh(now, time.Second) || v.Fresh(now.Add(time.Hour), time.Second) {
		t.Fatal("fresh")
	}
	v.Bid = 2
	v.Ask = 1
	if v.Validate() == nil {
		t.Fatal("crossed book")
	}
	v.Ask = 2
	v.Bid = 1
	v.Last = math.Inf(1)
	if v.Validate() == nil {
		t.Fatal("infinite")
	}
	if ValidSymbol("eth/usdt") || !ValidSymbol("1000PEPEUSDT") {
		t.Fatal("symbols")
	}
	if _, e := IntervalDuration("M"); e == nil {
		t.Fatal("month must not be treated as fixed")
	}
}
