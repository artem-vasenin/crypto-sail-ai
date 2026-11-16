package observer

import (
	"testing"
	"time"
)

func TestPostOnlyDoesNotAssumeFillFromTouch(t *testing.T) {
	o, e := NewPostOnlyOrder("x", "BTCUSDT", "LONG", now, 100, 100.1, 100, 1, 0.01, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if n, e := o.ObserveTrade(TradePrint{At: now.Add(time.Second), Price: 100, Qty: 3, AggressorSide: "SELL"}); e != nil || n != 0 || o.Status != "PENDING" {
		t.Fatal(n, e, o)
	}
	if n, e := o.ObserveTrade(TradePrint{At: now.Add(2 * time.Second), Price: 99.98, Qty: 4, AggressorSide: "BUY"}); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if n, e := o.ObserveTrade(TradePrint{At: now.Add(3 * time.Second), Price: 99.98, Qty: 4, AggressorSide: "SELL"}); e != nil || n <= 0 || o.Status != "PARTIALLY_FILLED" {
		t.Fatal(n, e, o)
	}
	if n, e := o.ObserveTrade(TradePrint{At: now.Add(4 * time.Second), Price: 99.98, Qty: 100, AggressorSide: "SELL"}); e != nil || n <= 0 || o.Status != "FILLED" || o.Filled != 1 {
		t.Fatal(n, e, o)
	}
	if n, _ := o.ObserveTrade(TradePrint{At: now.Add(5 * time.Second), Price: 90, Qty: 10, AggressorSide: "SELL"}); n != 0 {
		t.Fatal("duplicate fill")
	}
}
func TestPostOnlyGuardsAndExpiry(t *testing.T) {
	if _, e := NewPostOnlyOrder("x", "BTCUSDT", "LONG", now, 100, 100.1, 100.1, 1, .01, time.Minute); e == nil {
		t.Fatal("buy crossing spread")
	}
	if _, e := NewPostOnlyOrder("x", "BTCUSDT", "SHORT", now, 100, 100.1, 100, 1, .01, time.Minute); e == nil {
		t.Fatal("sell crossing spread")
	}
	if _, e := NewPostOnlyOrder("x", "BTCUSDT", "LONG", now, 100, 100.1, 99.995, 1, .01, time.Minute); e == nil {
		t.Fatal("tick misalignment")
	}
	o, e := NewPostOnlyOrder("x", "BTCUSDT", "SHORT", now, 100, 100.1, 100.1, 1, .01, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := o.ObserveTrade(TradePrint{At: now, Price: 100.2, Qty: 10, AggressorSide: "BUY"}); e == nil {
		t.Fatal("pre-placement trade")
	}
	if n, e := o.ObserveTrade(TradePrint{At: now.Add(time.Minute), Price: 100.3, Qty: 10, AggressorSide: "BUY"}); e != nil || n != 0 || o.Status != "EXPIRED" {
		t.Fatal(n, e, o)
	}
	if e := o.Cancel(now.Add(2 * time.Minute)); e == nil {
		t.Fatal("expired cancelled")
	}
}
