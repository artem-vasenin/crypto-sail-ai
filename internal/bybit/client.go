// Package bybit is a PUBLIC REST-only Bybit V5 client. It intentionally has no API keys or order endpoints.
package bybit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"crypto-sail-ai/internal/market"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
	Sleeper func(context.Context, time.Duration) error
}

func New(base string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}, Now: time.Now, Sleeper: sleep}
}
func sleep(ctx context.Context, d time.Duration) error {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}

type envelope struct {
	RetCode int             `json:"retCode"`
	RetMsg  string          `json:"retMsg"`
	Time    int64           `json:"time"`
	Result  json.RawMessage `json:"result"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) (time.Time, error) {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	if c.Sleeper == nil {
		c.Sleeper = sleep
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		u := c.BaseURL + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return time.Time{}, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = err
		} else {
			b, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			resp.Body.Close()
			if readErr != nil {
				return time.Time{}, readErr
			}
			if resp.StatusCode == 200 {
				var e envelope
				if err := json.Unmarshal(b, &e); err != nil {
					return time.Time{}, fmt.Errorf("bybit invalid json: %w", err)
				}
				if e.RetCode == 0 {
					if len(e.Result) == 0 {
						return time.Time{}, errors.New("bybit missing result")
					}
					if err := json.Unmarshal(e.Result, out); err != nil {
						return time.Time{}, err
					}
					if e.Time > 0 {
						return time.UnixMilli(e.Time).UTC(), nil
					}
					return c.Now().UTC(), nil
				}
				// documented transient rate-limit retCode; all other API errors are not retried.
				if e.RetCode != 10006 {
					return time.Time{}, fmt.Errorf("bybit retCode=%d: %s", e.RetCode, e.RetMsg)
				}
				last = fmt.Errorf("rate limit %d", e.RetCode)
			} else if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
				last = fmt.Errorf("HTTP %d", resp.StatusCode)
			} else {
				return time.Time{}, fmt.Errorf("bybit HTTP %d: %s", resp.StatusCode, string(b[:min(len(b), 256)]))
			}
		}
		if attempt < 2 {
			var jitter [2]byte
			_, _ = rand.Read(jitter[:])
			ms := binary.BigEndian.Uint16(jitter[:]) % 90
			wait := time.Duration(150*(1<<attempt)+int(ms)) * time.Millisecond
			if err := c.Sleeper(ctx, wait); err != nil {
				return time.Time{}, err
			}
		}
	}
	return time.Time{}, fmt.Errorf("bybit retries exhausted: %w", last)
}
func number(v string) (float64, error) {
	if v == "" {
		return 0, nil
	}
	n, e := strconv.ParseFloat(v, 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("invalid number %q", v)
	}
	return n, nil
}
func requiredNumber(v string) (float64, error) {
	if v == "" {
		return 0, errors.New("missing number")
	}
	return number(v)
}
func parseInstrument(x instrumentRaw) (market.Instrument, error) {
	t, e := number(x.PriceFilter.TickSize)
	if e != nil {
		return market.Instrument{}, e
	}
	step, e := number(x.LotSizeFilter.QtyStep)
	if e != nil {
		return market.Instrument{}, e
	}
	qty, e := number(x.LotSizeFilter.MinOrderQty)
	if e != nil {
		return market.Instrument{}, e
	}
	notional, e := number(x.LotSizeFilter.MinNotionalValue)
	if e != nil {
		return market.Instrument{}, e
	}
	var launch time.Time
	if x.LaunchTime != "" {
		ms, e := strconv.ParseInt(x.LaunchTime, 10, 64)
		if e != nil {
			return market.Instrument{}, e
		}
		launch = time.UnixMilli(ms).UTC()
	}
	return market.Instrument{Symbol: x.Symbol, Status: x.Status, ContractType: x.ContractType, QuoteCoin: x.QuoteCoin, SettleCoin: x.SettleCoin, LaunchTime: launch, TickSize: t, QtyStep: step, MinQty: qty, MinNotional: notional}, nil
}

type instrumentRaw struct {
	Symbol       string `json:"symbol"`
	Status       string `json:"status"`
	ContractType string `json:"contractType"`
	QuoteCoin    string `json:"quoteCoin"`
	SettleCoin   string `json:"settleCoin"`
	LaunchTime   string `json:"launchTime"`
	PriceFilter  struct {
		TickSize string `json:"tickSize"`
	} `json:"priceFilter"`
	LotSizeFilter struct {
		QtyStep          string `json:"qtyStep"`
		MinOrderQty      string `json:"minOrderQty"`
		MinNotionalValue string `json:"minNotionalValue"`
	} `json:"lotSizeFilter"`
}

// Instruments returns complete Trading LinearPerpetual USDT universe; a duplicate cursor fails closed.
func (c *Client) Instruments(ctx context.Context) ([]market.Instrument, error) {
	seenCursor := map[string]bool{}
	symbols := map[string]bool{}
	var all []market.Instrument
	cursor := ""
	for page := 0; page < 100; page++ {
		q := url.Values{"category": {"linear"}, "limit": {"1000"}, "status": {"Trading"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var p struct {
			Next string          `json:"nextPageCursor"`
			List []instrumentRaw `json:"list"`
		}
		if _, err := c.get(ctx, "/v5/market/instruments-info", q, &p); err != nil {
			return nil, err
		}
		for _, x := range p.List {
			if x.Status != "Trading" || x.ContractType != "LinearPerpetual" || x.QuoteCoin != "USDT" || x.SettleCoin != "USDT" {
				continue
			}
			v, e := parseInstrument(x)
			if e != nil {
				return nil, fmt.Errorf("instrument %s: %w", x.Symbol, e)
			}
			if !market.ValidSymbol(v.Symbol) || v.TickSize <= 0 || v.QtyStep <= 0 {
				return nil, fmt.Errorf("invalid instrument %q", v.Symbol)
			}
			if symbols[v.Symbol] {
				return nil, fmt.Errorf("duplicate instrument %s", v.Symbol)
			}
			symbols[v.Symbol] = true
			all = append(all, v)
		}
		if p.Next == "" {
			sort.Slice(all, func(i, j int) bool { return all[i].Symbol < all[j].Symbol })
			return all, nil
		}
		if seenCursor[p.Next] {
			return nil, errors.New("repeated pagination cursor")
		}
		seenCursor[p.Next] = true
		cursor = p.Next
	}
	return nil, errors.New("too many pagination pages")
}

type tickerRaw struct {
	Symbol   string `json:"symbol"`
	Last     string `json:"lastPrice"`
	Bid      string `json:"bid1Price"`
	Ask      string `json:"ask1Price"`
	BidSize  string `json:"bid1Size"`
	AskSize  string `json:"ask1Size"`
	Turnover string `json:"turnover24h"`
	Change   string `json:"price24hPcnt"`
	OI       string `json:"openInterest"`
	Funding  string `json:"fundingRate"`
}

func (c *Client) Tickers(ctx context.Context) ([]market.Ticker, error) {
	var p struct {
		List []tickerRaw `json:"list"`
	}
	timestamp, e := c.get(ctx, "/v5/market/tickers", url.Values{"category": {"linear"}}, &p)
	if e != nil {
		return nil, e
	}
	all := make([]market.Ticker, 0, len(p.List))
	seen := map[string]bool{}
	for _, x := range p.List {
		if seen[x.Symbol] {
			return nil, fmt.Errorf("duplicate ticker %s", x.Symbol)
		}
		seen[x.Symbol] = true
		values := []string{x.Last, x.Bid, x.Ask, x.BidSize, x.AskSize, x.Turnover, x.Change, x.OI, x.Funding}
		ns := make([]float64, len(values))
		for i, v := range values {
			var err error
			ns[i], err = number(v)
			if err != nil {
				return nil, fmt.Errorf("%s field %d: %w", x.Symbol, i, err)
			}
		}
		t := market.Ticker{Symbol: x.Symbol, Last: ns[0], Bid: ns[1], Ask: ns[2], BidSize: ns[3], AskSize: ns[4], Turnover24h: ns[5], Change24h: ns[6], OI: ns[7], Funding: ns[8], OIAvailable: x.OI != "", FundingAvailable: x.Funding != "", ObservedAt: timestamp}
		// Invalid individual tickers are rejected downstream by surface rather than hiding the symbol.
		all = append(all, t)
	}
	return all, nil
}

// Candles fetches up to 1000 bars, discards the forming candle, sorts ascending, and validates.
func (c *Client) Candles(ctx context.Context, symbol, tf string, limit int) ([]market.Candle, error) {
	if !market.ValidSymbol(symbol) {
		return nil, errors.New("invalid symbol")
	}
	dur, e := market.IntervalDuration(tf)
	if e != nil {
		return nil, e
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("limit must be 1..1000")
	}
	// Request one extra candle because the latest may still be open.
	n := limit + 1
	if n > 1000 {
		n = 1000
	}
	q := url.Values{"category": {"linear"}, "symbol": {symbol}, "interval": {tf}, "limit": {strconv.Itoa(n)}}
	var p struct {
		Symbol   string     `json:"symbol"`
		Category string     `json:"category"`
		List     [][]string `json:"list"`
	}
	_, e = c.get(ctx, "/v5/market/kline", q, &p)
	if e != nil {
		return nil, e
	}
	if p.Symbol != symbol || p.Category != "linear" {
		return nil, errors.New("kline symbol/category mismatch")
	}
	now := c.Now().UTC()
	var out []market.Candle
	seen := map[int64]bool{}
	for _, r := range p.List {
		if len(r) != 7 {
			return nil, errors.New("unexpected kline width")
		}
		ms, e := strconv.ParseInt(r[0], 10, 64)
		if e != nil {
			return nil, e
		}
		start := time.UnixMilli(ms).UTC()
		if start.Add(dur).After(now) {
			continue
		}
		if seen[ms] {
			return nil, errors.New("duplicate candle")
		}
		seen[ms] = true
		nums := make([]float64, 6)
		for i := range nums {
			nums[i], e = requiredNumber(r[i+1])
			if e != nil {
				return nil, e
			}
		}
		out = append(out, market.Candle{OpenTime: start, Open: nums[0], High: nums[1], Low: nums[2], Close: nums[3], Volume: nums[4], Turnover: nums[5]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpenTime.Before(out[j].OpenTime) })
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	if len(out) == 0 {
		return nil, errors.New("no closed candles")
	}
	if e := market.ValidateCandles(out, tf, now, false); e != nil {
		return nil, e
	}
	return out, nil
}

// SnapshotID is a short content-derived identifier for one immutable input payload.
func SnapshotID(v []byte) string { sum := sha256.Sum256(v); return fmt.Sprintf("%x", sum[:12]) }
