package features

import (
	"crypto-sail-ai/internal/market"
	"math"
	"testing"
	"time"
)

func bars(n int, fn func(int) float64) []market.Candle {
	cs := make([]market.Candle, n)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		v := fn(i)
		cs[i] = market.Candle{OpenTime: start.Add(time.Duration(i) * time.Hour), Open: v, Close: v, High: v + 1, Low: v - 1, Volume: 5, Turnover: v * 5}
	}
	return cs
}
func TestIndicatorsAndMissing(t *testing.T) {
	cs := bars(40, func(i int) float64 { return 100 })
	if !SMA(cs, 20).Valid || math.Abs(SMA(cs, 20).Value-100) > 1e-8 {
		t.Fatal("SMA")
	}
	if EMA(cs, 20).Value != 100 || RSI(cs, 14).Value != 50 || ATR(cs, 14).Value != 2 {
		t.Fatal("flat indicators")
	}
	if SMA(cs, 200).Valid || SMA(cs, 200).Reason != "insufficient_history" {
		t.Fatal("missing warmup")
	}
	up := bars(50, func(i int) float64 { return float64(100 + i) })
	if RSI(up, 14).Value != 100 {
		t.Fatal("RSI gains")
	}
	down := bars(50, func(i int) float64 { return float64(150 - i) })
	if RSI(down, 14).Value != 0 {
		t.Fatal("RSI losses")
	}
	if _, e := Build("60", nil); e == nil {
		t.Fatal("empty accepted")
	}
	if x, e := Build("60", up); e != nil || !x.ATRPercent.Valid || x.Count != 50 {
		t.Fatalf("%+v %v", x, e)
	}
}
func TestSwingRegimes(t *testing.T) {
	flat := bars(20, func(i int) float64 { return 100 })
	s := Swings(flat, 2)
	if s.Regime != "UNKNOWN" {
		t.Fatalf("%+v", s)
	}
	// Explicit pivot construction: high and low in separated peaks/valleys.
	prices := []float64{10, 11, 12, 18, 13, 11, 9, 11, 12, 20, 14, 12, 10, 11, 12, 22, 15, 13, 11, 12, 13}
	cs := bars(len(prices), func(i int) float64 { return prices[i] })
	s = Swings(cs, 1)
	if s.HighCount < 2 || s.LowCount < 2 || s.HighPattern != "HH" || s.LowPattern != "HL" || s.Regime != "BULL" {
		t.Fatalf("%+v", s)
	}
	if x := Swings(cs[:3], 2); x.Regime != "UNKNOWN" {
		t.Fatal(x)
	}
}
