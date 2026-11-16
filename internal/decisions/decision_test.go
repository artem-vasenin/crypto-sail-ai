package decisions

import (
	"crypto-sail-ai/internal/features"
	"crypto-sail-ai/internal/surface"
	"testing"
)

func TestProposalAndHardGates(t *testing.T) {
	p := Proposal{SnapshotID: "abc", Symbol: "BTCUSDT", Side: "LONG", Decision: Ready, Reasons: []string{"good_setup"}, Explanation: "conditional", EntryLow: 99, EntryHigh: 100, InvalidateAt: 98}
	if e := p.Validate("abc", "BTCUSDT"); e != nil {
		t.Fatal(e)
	}
	tfs := map[string]features.Timeframe{}
	for _, tf := range []string{"15", "60", "240"} {
		tfs[tf] = features.Timeframe{Count: 70, ATR14: features.Available(1), Structure: features.Structure{Regime: "BULL"}}
	}
	s := surface.Result{Symbol: "BTCUSDT", Status: surface.Candidate}
	if d, _ := Gate(p, s, tfs); d != Ready {
		t.Fatalf("expected ready, got %s", d)
	}
	s.Status = surface.Watch
	if d, _ := Gate(p, s, tfs); d != Wait {
		t.Fatal(d)
	}
	s.Status = surface.Reject
	if d, _ := Gate(p, s, tfs); d != Reject {
		t.Fatal(d)
	}
	s.Status = surface.Candidate
	delete(tfs, "240")
	if d, _ := Gate(p, s, tfs); d != Wait {
		t.Fatal(d)
	}
	p.InvalidateAt = 101
	if d, _ := Gate(p, s, tfs); d != Wait {
		t.Fatal(d)
	}
	p.Decision = Ready
	p.Side = "NONE"
	if p.Validate("abc", "BTCUSDT") == nil {
		t.Fatal("READY NONE")
	}
	p.Side = "LONG"
	if p.Validate("other", "BTCUSDT") == nil {
		t.Fatal("snapshot mismatch")
	}
}
