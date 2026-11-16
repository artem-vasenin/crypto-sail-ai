package storage

import (
	"crypto-sail-ai/internal/watch"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLitePersistenceMigrationBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db", "sail.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if e = s.SaveSnapshot("id1", "surface", "BTCUSDT", now, map[string]any{"price": 100}); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSnapshot("id1", "surface", "BTCUSDT", now, map[string]any{"price": 200}); e == nil {
		t.Fatal("duplicate changed immutable snapshot")
	}
	if e = s.SaveDecision("id1", "surface", "BTCUSDT", "WATCH", now, map[string]any{"reason": "wide_spread"}); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveDecision("missing", "surface", "BTCUSDT", "WATCH", now, nil); e == nil {
		t.Fatal("foreign key not active")
	}
	idea := watch.Idea{Symbol: "BTCUSDT", Side: "LONG", SnapshotID: "id1", Status: "WAIT", NextCheck: now.Add(time.Hour), Expires: now.Add(2 * time.Hour)}
	if e = s.UpsertIdea(idea); e != nil {
		t.Fatal(e)
	}
	idea.EntryLow = 95
	idea.EntryHigh = 99
	if e = s.UpsertIdea(idea); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveLLM("id1", "triage", "qwen", false, 100, 10, 0, "", "timeout"); e != nil {
		t.Fatal(e)
	}
	if e = s.SavePosition("virtual-1", map[string]any{"status": "CLOSED"}); e != nil {
		t.Fatal(e)
	}
	if e = s.SavePositionEvent("virtual-1", now, map[string]string{"event": "SL"}); e != nil {
		t.Fatal(e)
	}
	if e = s.Integrity(); e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(t.TempDir(), "copy.db")
	if e = s.Backup(out); e != nil {
		t.Fatal(e)
	}
	if e = s.Backup(out); e == nil {
		t.Fatal("backup overwritten")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	for _, f := range []string{path, out} {
		db, e := Open(f)
		if e != nil {
			t.Fatal(e)
		}
		rows, e := db.Latest("surface", 5)
		if e != nil || len(rows) != 1 {
			t.Fatalf("%v %v", rows, e)
		}
		var x map[string]any
		if e = json.Unmarshal(rows[0], &x); e != nil || x["price"] != float64(100) {
			t.Fatal(x, e)
		}
		ideas, e := db.Ideas()
		if e != nil || len(ideas) != 1 || ideas[0].EntryLow != 95 {
			t.Fatal(ideas, e)
		}
		counts, e := db.Counts()
		if e != nil || counts["snapshots"] != 1 || counts["decisions"] != 1 || counts["position_events"] != 1 {
			t.Fatal(counts, e)
		}
		if e = db.Integrity(); e != nil {
			t.Fatal(e)
		}
		db.Close()
	}
}
func TestSQLiteConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := s.SaveLLM("sid", "triage", "qwen", true, 1, 2, 3, "{}", ""); e != nil {
				t.Errorf("write %d: %v", i, e)
			}
		}(i)
	}
	wg.Wait()
	counts, e := s.Counts()
	if e != nil || counts["llm_calls"] != 12 {
		t.Fatal(counts, e)
	}
}
func TestStorageSafety(t *testing.T) {
	if _, e := Open(""); e == nil {
		t.Fatal("empty db path accepted")
	}
	p := filepath.Join(t.TempDir(), "s.db")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.SaveSnapshot("", "surface", "BTCUSDT", time.Now(), nil); e == nil {
		t.Fatal("incomplete snapshot")
	}
	if _, e = s.Latest("surface", 0); e == nil {
		t.Fatal("zero limit")
	}
	if e = s.Backup(p); e == nil {
		t.Fatal("self-backup")
	}
	if _, e = os.Stat(p); e != nil {
		t.Fatal(e)
	}
}
