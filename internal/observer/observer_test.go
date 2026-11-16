package observer

import (
	"crypto-sail-ai/internal/market"
	"math"
	"testing"
	"time"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func bar(mins int, open, hi, lo, close float64) market.Candle {
	return market.Candle{OpenTime: now.Add(time.Duration(mins) * time.Minute), Open: open, High: hi, Low: lo, Close: close, Volume: 1, Turnover: 100}
}
func TestSizingAndRiskGuards(t *testing.T) {
	n, e := PositionSize(5, 100, 98)
	if e != nil || n != 2.5 {
		t.Fatal(n, e)
	}
	if _, e = PositionSize(0, 100, 98); e == nil {
		t.Fatal("zero risk accepted")
	}
	r := DefaultRules()
	if _, e = NewFilled("x", "BTCUSDT", "LONG", now, 100, 101, 110, 1, r); e == nil {
		t.Fatal("SL invalid")
	}
	if _, e = NewFilled("x", "BTCUSDT", "LONG", now, 100, 99, 100.5, 1, r); e == nil {
		t.Fatal("fees exceed net target")
	}
	if _, e = NewFilled("x", "BTCUSDT", "SHORT", now, 100, 101, 98, 1, r); e != nil {
		t.Fatal(e)
	}
	r.TickSize = 0
	if r.Validate() == nil {
		t.Fatal("zero tick accepted")
	}
}
func TestBreakevenLongAndShortCoverCosts(t *testing.T) {
	r := DefaultRules()
	for _, side := range []string{"LONG", "SHORT"} {
		stop, tp := 98., 105.
		if side == "SHORT" {
			stop, tp = 102, 95
		}
		p, e := NewFilled("x", "BTCUSDT", side, now, 100, stop, tp, 1, r)
		if e != nil {
			t.Fatal(e)
		}
		be := p.BreakevenStop()
		if p.pnl(be, true) < -1e-8 {
			t.Fatalf("%s cost breakeven loss=%v at %v", side, p.pnl(be, true), be)
		}
		if side == "LONG" && be <= 100 || side == "SHORT" && be >= 100 {
			t.Fatalf("not cost-aware %s %.4f", side, be)
		}
	}
}
func TestConservativeStopOnAmbiguousBar(t *testing.T) {
	p, e := NewFilled("x", "BTCUSDT", "LONG", now, 100, 98, 105, 1, DefaultRules())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.OnClosedBar(bar(5, 100, 106, 97, 104), 1); e != nil {
		t.Fatal(e)
	}
	if p.Status != "CLOSED" || p.ExitReason != "SL" || p.NetR >= -1 || len(p.Events) < 3 {
		t.Fatalf("%+v", p)
	}
	if p.InitialRiskPrice != 2 {
		t.Fatal("initial R changed")
	}
	if e = p.OnClosedBar(bar(10, 100, 101, 99, 100), 1); e == nil {
		t.Fatal("double close")
	}
}
func TestGapWorseThanStop(t *testing.T) {
	p, _ := NewFilled("x", "BTCUSDT", "LONG", now, 100, 98, 105, 1, DefaultRules())
	p.OnClosedBar(bar(5, 96, 97, 95, 96), 1)
	if p.ExitPrice != 96 {
		t.Fatal(p.ExitPrice)
	}
	q, _ := NewFilled("y", "BTCUSDT", "SHORT", now, 100, 102, 95, 1, DefaultRules())
	q.OnClosedBar(bar(5, 104, 105, 103, 104), 1)
	if q.ExitPrice != 104 {
		t.Fatal(q.ExitPrice)
	}
}
func TestBEOnlyAfterBarAndSLMonotonic(t *testing.T) {
	r := DefaultRules()
	p, _ := NewFilled("x", "BTCUSDT", "LONG", now, 100, 98, 110, 1, r)
	p.OnClosedBar(bar(5, 100, 103, 99, 102.3), 0.4)
	if !p.BEApplied || p.CurrentSL <= 100 || p.InitialRiskPrice != 2 {
		t.Fatalf("%+v", p)
	}
	prev := p.CurrentSL
	if p.ImproveStop(now.Add(time.Minute), prev-1, 103, "bad") {
		t.Fatal("SL worsened")
	}
	p.OnClosedBar(bar(10, 102.3, 105, 102, 104.5), 0.5)
	if p.CurrentSL < prev || p.Status != "OPEN" {
		t.Fatal(p.CurrentSL)
	}
	p.OnClosedBar(bar(15, 101, 102, 99, 101), 1)
	if p.ExitReason != "SL" || p.InitialRiskUSDT != 2 {
		t.Fatalf("%+v", p)
	}
	q, _ := NewFilled("y", "BTCUSDT", "SHORT", now, 100, 102, 90, 1, r)
	q.OnClosedBar(bar(5, 100, 101, 97, 97.7), 0.4)
	if !q.BEApplied || q.CurrentSL >= 100 {
		t.Fatal(q.CurrentSL)
	}
	if q.ImproveStop(now.Add(time.Minute), q.CurrentSL+1, 98, "bad") {
		t.Fatal("SHORT SL worsened")
	}
}
func TestNoFutureBarReuseAndPostExit(t *testing.T) {
	p, _ := NewFilled("x", "BTCUSDT", "LONG", now, 100, 98, 105, 1, DefaultRules())
	if e := p.OnClosedBar(bar(0, 100, 101, 99, 100), 0); e == nil {
		t.Fatal("entry bar reused")
	}
	if e := p.OnClosedBar(bar(5, 100, 101, 99, 100), 0); e != nil {
		t.Fatal(e)
	}
	if e := p.OnClosedBar(bar(5, 100, 101, 99, 100), 0); e == nil {
		t.Fatal("duplicate bar")
	}
	if _, e := PostExit(p, map[int]float64{5: 105}); e == nil {
		t.Fatal("open post-exit")
	}
	p.OnClosedBar(bar(10, 102, 106, 101, 105), 0)
	ps, e := PostExit(p, map[int]float64{5: 106, 60: 108})
	if e != nil || len(ps) != 2 || math.Abs(ps[0].FromExitPct-0.95238095) > 1e-4 {
		t.Fatal(ps, e)
	}
}
