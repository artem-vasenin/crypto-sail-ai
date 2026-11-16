package surface

import (
	"crypto-sail-ai/internal/market"
	"testing"
	"time"
)

func good(now time.Time) (market.Ticker, market.Instrument) {
	return market.Ticker{Symbol: "TINYUSDT", Last: 0.0001, Bid: 0.0000999, Ask: 0.0001001, BidSize: 100, AskSize: 200, Turnover24h: 1e6, ObservedAt: now}, market.Instrument{Symbol: "TINYUSDT", Status: "Trading", ContractType: "LinearPerpetual", QuoteCoin: "USDT", SettleCoin: "USDT", LaunchTime: now.Add(-24 * time.Hour)}
}
func TestSurfaceClassifiesWithoutNominalPriceRule(t *testing.T) {
	now := time.Now().UTC()
	tk, ins := good(now)
	cfg := Settings{MinTurnover: 100000, MaxSpreadPct: .5, MaxAge: time.Minute}
	r := Evaluate(tk, ins, now, cfg)
	if r.Status != Candidate || r.HistoryClass != "new" {
		t.Fatalf("%+v", r)
	}
	t.Run("low turnover watch", func(t *testing.T) {
		x := tk
		x.Turnover24h = 10
		if z := Evaluate(x, ins, now, cfg); z.Status != Watch {
			t.Fatal(z)
		}
	})
	t.Run("wide spread watch", func(t *testing.T) {
		x := tk
		x.Ask = 0.00012
		if z := Evaluate(x, ins, now, cfg); z.Status != Watch {
			t.Fatal(z)
		}
	})
	t.Run("stale reject", func(t *testing.T) {
		x := tk
		x.ObservedAt = now.Add(-time.Hour)
		if z := Evaluate(x, ins, now, cfg); z.Status != Reject {
			t.Fatal(z)
		}
	})
	t.Run("malformed reject", func(t *testing.T) {
		x := tk
		x.Bid = x.Ask * 2
		if z := Evaluate(x, ins, now, cfg); z.Status != Reject {
			t.Fatal(z)
		}
	})
	t.Run("wrong instrument reject", func(t *testing.T) {
		x := ins
		x.SettleCoin = "USDC"
		if z := Evaluate(tk, x, now, cfg); z.Status != Reject {
			t.Fatal(z)
		}
	})
}
