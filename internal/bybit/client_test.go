package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func api(w http.ResponseWriter, res any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"retCode": 0, "retMsg": "OK", "time": time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC).UnixMilli(), "result": res})
}
func sampleInstrument(symbol string) map[string]any {
	return map[string]any{"symbol": symbol, "status": "Trading", "contractType": "LinearPerpetual", "quoteCoin": "USDT", "settleCoin": "USDT", "launchTime": "1760000000000", "priceFilter": map[string]string{"tickSize": "0.01"}, "lotSizeFilter": map[string]string{"qtyStep": "0.001", "minOrderQty": "0.001", "minNotionalValue": "5"}}
}
func TestInstrumentPaginationAndFiltering(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v5/market/instruments-info" || r.URL.Query().Get("category") != "linear" {
			t.Errorf("unexpected url: %s", r.URL)
		}
		if r.URL.Query().Get("cursor") == "" {
			bad := sampleInstrument("ETHUSDC")
			bad["settleCoin"] = "USDC"
			api(w, map[string]any{"nextPageCursor": "next", "list": []any{sampleInstrument("BTCUSDT"), bad}})
		} else {
			api(w, map[string]any{"nextPageCursor": "", "list": []any{sampleInstrument("ETHUSDT")}})
		}
	}))
	defer ts.Close()
	c := New(ts.URL)
	out, e := c.Instruments(context.Background())
	if e != nil || len(out) != 2 || calls != 2 || out[0].Symbol != "BTCUSDT" {
		t.Fatalf("%+v %d %v", out, calls, e)
	}
}
func TestRepeatedCursorAndDuplicateInstrument(t *testing.T) {
	for _, name := range []string{"cursor", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "cursor" {
					api(w, map[string]any{"nextPageCursor": "A", "list": []any{}})
				} else {
					api(w, map[string]any{"nextPageCursor": "", "list": []any{sampleInstrument("BTCUSDT"), sampleInstrument("BTCUSDT")}})
				}
			}))
			defer ts.Close()
			if _, e := New(ts.URL).Instruments(context.Background()); e == nil {
				t.Fatal("must reject")
			}
		})
	}
}
func TestKlineClosedAndSorted(t *testing.T) {
	now := time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC)
	row := func(hoursAgo int) []string {
		return []string{strconv.FormatInt(now.Add(-time.Duration(hoursAgo)*time.Hour).UnixMilli(), 10), "100", "101", "99", "100.5", "10", "1005"}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("interval") != "60" {
			t.Error("bad interval")
		}
		api(w, map[string]any{"symbol": "BTCUSDT", "category": "linear", "list": [][]string{row(0), row(1), row(2)}})
	}))
	defer ts.Close()
	c := New(ts.URL)
	c.Now = func() time.Time { return now }
	cs, e := c.Candles(context.Background(), "BTCUSDT", "60", 3)
	if e != nil || len(cs) != 2 || !cs[0].OpenTime.Before(cs[1].OpenTime) {
		t.Fatalf("%+v %v", cs, e)
	}
	if _, e = c.Candles(context.Background(), "x invalid", "60", 3); e == nil {
		t.Fatal("symbol accepted")
	}
	if _, e = c.Candles(context.Background(), "BTCUSDT", "60", 1001); e == nil {
		t.Fatal("limit accepted")
	}
}
func TestPublicTickersAndInvalidOneRetained(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api(w, map[string]any{"list": []map[string]string{{"symbol": "BTCUSDT", "lastPrice": "100", "bid1Price": "99", "ask1Price": "101", "turnover24h": "1000000", "bid1Size": "3", "ask1Size": "4"}, {"symbol": "EMPTYUSDT", "lastPrice": "10", "bid1Price": "", "ask1Price": ""}}})
	}))
	defer ts.Close()
	v, e := New(ts.URL).Tickers(context.Background())
	if e != nil || len(v) != 2 || v[0].Turnover24h != 1e6 || v[0].OIAvailable || v[0].FundingAvailable || v[1].Validate() == nil {
		t.Fatalf("%v %+v", e, v)
	}
}
func TestRetriesAndAPIError(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, "rate limit")
			return
		}
		api(w, map[string]any{"list": []any{}})
	}))
	defer ts.Close()
	c := New(ts.URL)
	c.Sleeper = func(ctx context.Context, d time.Duration) error { return nil }
	if _, e := c.Tickers(context.Background()); e != nil || n.Load() != 2 {
		t.Fatalf("retries: %d %v", n.Load(), e)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"retCode":10001,"retMsg":"wrong category","result":{},"time":1}`)
	}))
	defer bad.Close()
	if _, e := New(bad.URL).Tickers(context.Background()); e == nil || !strings.Contains(e.Error(), "10001") {
		t.Fatal("expected retCode error", e)
	}
}
func TestInvalidKlineAndNonfinite(t *testing.T) {
	if _, e := number("NaN"); e == nil {
		t.Fatal("NaN accepted")
	}
	if _, e := requiredNumber(""); e == nil {
		t.Fatal("missing accepted")
	}
	now := time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api(w, map[string]any{"symbol": "ETHUSDT", "category": "linear", "list": [][]string{{strconv.FormatInt(now.Add(-time.Hour).UnixMilli(), 10), "100", "90", "99", "100", "1", "100"}}})
	}))
	defer ts.Close()
	c := New(ts.URL)
	c.Now = func() time.Time { return now }
	if _, e := c.Candles(context.Background(), "ETHUSDT", "60", 1); e == nil {
		t.Fatal("bad OHLC accepted")
	}
}

func TestTickerAvailabilityDistinguishesZeroFromUnknown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api(w, map[string]any{"list": []map[string]string{
			{"symbol": "BTCUSDT", "lastPrice": "100", "bid1Price": "99", "ask1Price": "101", "turnover24h": "10000", "openInterest": "0", "fundingRate": "0"},
			{"symbol": "ETHUSDT", "lastPrice": "100", "bid1Price": "99", "ask1Price": "101", "turnover24h": "10000"},
		}})
	}))
	defer ts.Close()
	ticks, err := New(ts.URL).Tickers(context.Background())
	if err != nil || len(ticks) != 2 {
		t.Fatalf("%v %+v", err, ticks)
	}
	if !ticks[0].OIAvailable || !ticks[0].FundingAvailable || ticks[1].OIAvailable || ticks[1].FundingAvailable {
		t.Fatalf("missing and true-zero derivative fields must be distinct: %+v", ticks)
	}
}
