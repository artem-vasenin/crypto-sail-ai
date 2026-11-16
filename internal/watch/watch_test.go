package watch

import (
	"testing"
	"time"
)

func TestRecheckNeverMeansEnter(t *testing.T) {
	now := time.Now().UTC()
	i := Idea{Symbol: "BTCUSDT", Side: "LONG", SnapshotID: "s", Status: "WAIT", EntryLow: 95, EntryHigh: 98, InvalidateAt: 90, NextCheck: now.Add(time.Minute), Expires: now.Add(time.Hour)}
	if e := i.Validate(); e != nil {
		t.Fatal(e)
	}
	if ok, reason := i.ShouldRecheck(now, 96); ok || reason != "cooldown" {
		t.Fatal(reason)
	}
	if ok, reason := i.ShouldRecheck(now.Add(2*time.Minute), 96); !ok || reason != "entry_zone" {
		t.Fatal(reason)
	}
	if ok, reason := i.ShouldRecheck(now.Add(2*time.Minute), 99); !ok || reason != "scheduled_recheck" {
		t.Fatal(reason)
	}
	if ok, reason := i.ShouldRecheck(now.Add(2*time.Minute), 85); ok || reason != "invalidated" {
		t.Fatal(reason)
	}
	if ok, reason := i.ShouldRecheck(now.Add(2*time.Hour), 96); ok || reason != "expired" {
		t.Fatal(reason)
	}
	i.EntryHigh = 92
	if i.Validate() == nil {
		t.Fatal("bad zone accepted")
	}
}
