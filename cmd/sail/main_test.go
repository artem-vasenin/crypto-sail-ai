package main

import (
	"bytes"
	"crypto-sail-ai/internal/config"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		var result any
		switch r.URL.Path {
		case "/v5/market/instruments-info":
			result = map[string]any{"list": []any{map[string]any{"symbol": "BTCUSDT", "contractType": "LinearPerpetual", "status": "Trading", "quoteCoin": "USDT", "settleCoin": "USDT", "launchTime": "1700000000000", "priceFilter": map[string]string{"tickSize": "0.01"}, "lotSizeFilter": map[string]string{"qtyStep": "0.001", "minOrderQty": "0.001"}}, map[string]any{"symbol": "NOQUOTEUSDT", "contractType": "LinearPerpetual", "status": "Trading", "quoteCoin": "USDT", "settleCoin": "USDT", "priceFilter": map[string]string{"tickSize": "0.01"}, "lotSizeFilter": map[string]string{"qtyStep": "0.001"}}}, "nextPageCursor": ""}
		case "/v5/market/tickers":
			result = map[string]any{"list": []any{map[string]string{"symbol": "BTCUSDT", "lastPrice": "100", "bid1Price": "99.99", "ask1Price": "100.01", "bid1Size": "5", "ask1Size": "5", "turnover24h": "10000000"}}}
		case "/v5/market/kline":
			tf := r.URL.Query().Get("interval")
			dur := time.Hour
			switch tf {
			case "5":
				dur = 5 * time.Minute
			case "15":
				dur = 15 * time.Minute
			case "30":
				dur = 30 * time.Minute
			case "60":
				dur = time.Hour
			case "240":
				dur = 4 * time.Hour
			}
			latestClosed := time.Unix(0, (now.UnixNano()/int64(dur))*int64(dur)).UTC().Add(-dur)
			var rows [][]string
			for i := 0; i < 102; i++ {
				start := latestClosed.Add(-time.Duration(i) * dur)
				v := 100. + float64(i%6)
				rows = append(rows, []string{strconv.FormatInt(start.UnixMilli(), 10), fmt.Sprintf("%.2f", v), fmt.Sprintf("%.2f", v+2), fmt.Sprintf("%.2f", v-2), fmt.Sprintf("%.2f", v+0.5), "5", "500"})
			}
			result = map[string]any{"symbol": r.URL.Query().Get("symbol"), "category": "linear", "list": rows}
		default:
			t.Errorf("unknown Bybit endpoint: %s", r.URL)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"retCode": 0, "retMsg": "OK", "result": result, "time": now.UnixMilli()})
	}))
}
func fixtureConfig(t *testing.T, base string) (string, string) {
	t.Helper()
	folder := t.TempDir()
	c := config.Default()
	c.BybitURL = base
	c.Database = filepath.Join(folder, "data.db")
	b, _ := json.Marshal(c)
	path := filepath.Join(folder, "config.json")
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	return path, c.Database
}
func command(t *testing.T, argv ...string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if e := run(argv, &out); e != nil {
		t.Fatalf("command %v error: %v", argv, e)
	}
	var res map[string]any
	if e := json.Unmarshal(out.Bytes(), &res); e != nil {
		t.Fatalf("bad JSON: %v\n%s", e, out.String())
	}
	return res
}
func TestCLIHelpDoctorAndOfflinePaper(t *testing.T) {
	var b bytes.Buffer
	if e := run([]string{"help"}, &b); e != nil || !bytes.Contains(b.Bytes(), []byte("paper")) {
		t.Fatal(e, b.String())
	}
	cp, db := fixtureConfig(t, "http://127.0.0.1:9876")
	v := command(t, "doctor", "-config", cp)
	if v["sqlite_integrity"] != "ok" || v["paper_only"] != true {
		t.Fatal(v)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fixture := map[string]any{"symbol": "BTCUSDT", "side": "LONG", "entry_at": base, "entry": 100, "stop": 98, "take": 105, "qty": 1, "atr": .5, "bars": []any{map[string]any{"open_time": base.Add(5 * time.Minute), "open": 100, "high": 106, "low": 99, "close": 105, "volume": 1, "turnover": 100}}}
	raw, _ := json.Marshal(fixture)
	file := filepath.Join(t.TempDir(), "fixture.json")
	if e := os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	v = command(t, "paper", "-config", cp, "-file", file)
	p := v["position"].(map[string]any)
	if p["status"] != "CLOSED" || p["exit_reason"] != "TP" || p["source_snapshot_id"] == nil {
		t.Fatal(v)
	}
	v = command(t, "status", "-config", cp)
	counts := v["counts"].(map[string]any)
	if counts["virtual_positions"] != float64(1) {
		t.Fatal(v)
	}
	out := filepath.Join(t.TempDir(), "backup.db")
	v = command(t, "backup", "-config", cp, "-out", out)
	if v["status"] != "ok" {
		t.Fatal(v)
	}
	command(t, "db-check", "-config", cp, "-db", out)
	if _, e := os.Stat(db); e != nil {
		t.Fatal(e)
	}
	if e := run([]string{"unknown"}, &bytes.Buffer{}); e == nil {
		t.Fatal("unknown command accepted")
	}
}
func TestCLICollectSurfaceDeepMockExchange(t *testing.T) {
	ts := testServer(t)
	defer ts.Close()
	cp, _ := fixtureConfig(t, ts.URL)
	v := command(t, "collect", "-config", cp, "-symbol", "BTCUSDT", "-tf", "60", "-limit", "3")
	if v["count"] != float64(3) {
		t.Fatal(v)
	}
	v = command(t, "surface", "-config", cp, "-show", "10")
	counts := v["counts"].(map[string]any)
	if counts["REJECT"] != float64(1) || counts["CANDIDATE"] != float64(1) || v["missing_tickers"] != float64(1) {
		t.Fatal(v)
	}
	v = command(t, "deep", "-config", cp, "-symbol", "BTCUSDT", "-side", "LONG")
	f := v["features"].(map[string]any)
	if _, ok := f["timeframes"].(map[string]any)["240"]; !ok {
		t.Fatal(v)
	}
	v = command(t, "status", "-config", cp)
	c := v["counts"].(map[string]any)
	if c["snapshots"] != float64(4) || c["decisions"] != float64(2) {
		t.Fatal(v)
	}
	if e := run([]string{"deep", "-config", cp, "-side", "BUY"}, &bytes.Buffer{}); e == nil {
		t.Fatal("invalid direction accepted")
	}
}
