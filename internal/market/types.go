// Package market describes validated Bybit public-market observations.
// It contains no order placement or private API logic.
package market

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"
)

var symbolRE = regexp.MustCompile(`^[A-Z0-9]{2,32}$`)

func ValidSymbol(s string) bool { return symbolRE.MatchString(s) }

// IntervalDuration supports fixed-duration periods. Calendar months are intentionally unsupported.
func IntervalDuration(tf string) (time.Duration, error) {
	switch tf {
	case "1":
		return time.Minute, nil
	case "3":
		return 3 * time.Minute, nil
	case "5":
		return 5 * time.Minute, nil
	case "15":
		return 15 * time.Minute, nil
	case "30":
		return 30 * time.Minute, nil
	case "60":
		return time.Hour, nil
	case "120":
		return 2 * time.Hour, nil
	case "240":
		return 4 * time.Hour, nil
	case "D":
		return 24 * time.Hour, nil
	case "W":
		return 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("unsupported timeframe %q", tf)
}

func Finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

type Candle struct {
	OpenTime time.Time `json:"open_time"`
	Open     float64   `json:"open"`
	High     float64   `json:"high"`
	Low      float64   `json:"low"`
	Close    float64   `json:"close"`
	Volume   float64   `json:"volume"`
	Turnover float64   `json:"turnover"`
}

func (c Candle) Validate() error {
	if c.OpenTime.IsZero() || c.OpenTime.Location() != time.UTC {
		return errors.New("candle timestamp must be UTC")
	}
	v := []float64{c.Open, c.High, c.Low, c.Close, c.Volume, c.Turnover}
	for _, x := range v {
		if !Finite(x) {
			return errors.New("non-finite candle value")
		}
	}
	if c.Open <= 0 || c.Close <= 0 || c.High <= 0 || c.Low <= 0 || c.Volume < 0 || c.Turnover < 0 || c.High < math.Max(c.Open, c.Close) || c.Low > math.Min(c.Open, c.Close) || c.Low > c.High {
		return errors.New("invalid OHLCV")
	}
	return nil
}

// ValidateCandles requires ascending, unique, aligned, CLOSED candles; gaps rejected when strict=true.
func ValidateCandles(cs []Candle, tf string, now time.Time, strict bool) error {
	dur, err := IntervalDuration(tf)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		return errors.New("empty candle set")
	}
	for i, c := range cs {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("candle %d: %w", i, err)
		}
		anchor := c.OpenTime
		if tf == "W" {
			anchor = c.OpenTime.Add(-4 * 24 * time.Hour)
		} // Bybit W starts Monday; Unix epoch was Thursday.
		if anchor.UnixNano()%int64(dur) != 0 {
			return fmt.Errorf("unaligned candle %d", i)
		}
		if c.OpenTime.Add(dur).After(now.UTC()) {
			return fmt.Errorf("unclosed candle %d", i)
		}
		if i > 0 {
			diff := c.OpenTime.Sub(cs[i-1].OpenTime)
			if diff <= 0 {
				return errors.New("out-of-order or repeated candle")
			}
			if strict && diff != dur {
				return fmt.Errorf("missing candles at %s", c.OpenTime)
			}
		}
	}
	return nil
}

type Instrument struct {
	Symbol       string    `json:"symbol"`
	Status       string    `json:"status"`
	ContractType string    `json:"contract_type"`
	QuoteCoin    string    `json:"quote_coin"`
	SettleCoin   string    `json:"settle_coin"`
	LaunchTime   time.Time `json:"launch_time"`
	TickSize     float64   `json:"tick_size"`
	QtyStep      float64   `json:"qty_step"`
	MinQty       float64   `json:"min_qty"`
	MinNotional  float64   `json:"min_notional"`
}

type Ticker struct {
	Symbol      string  `json:"symbol"`
	Last        float64 `json:"last"`
	Bid         float64 `json:"bid"`
	Ask         float64 `json:"ask"`
	BidSize     float64 `json:"bid_size"`
	AskSize     float64 `json:"ask_size"`
	Turnover24h float64 `json:"turnover_24h"`
	Change24h   float64 `json:"change_24h"`
	OI          float64 `json:"open_interest"`
	Funding     float64 `json:"funding"`
	// A missing derivatives field must not be interpreted as an actual zero.
	OIAvailable      bool      `json:"oi_available"`
	FundingAvailable bool      `json:"funding_available"`
	ObservedAt       time.Time `json:"observed_at"`
}

func (t Ticker) Validate() error {
	if !ValidSymbol(t.Symbol) {
		return errors.New("bad symbol")
	}
	if t.ObservedAt.IsZero() {
		return errors.New("missing ticker timestamp")
	}
	for _, x := range []float64{t.Last, t.Bid, t.Ask, t.BidSize, t.AskSize, t.Turnover24h, t.Change24h, t.OI, t.Funding} {
		if !Finite(x) {
			return errors.New("non-finite ticker")
		}
	}
	if t.Last <= 0 || t.Bid <= 0 || t.Ask <= 0 || t.Bid > t.Ask || t.BidSize < 0 || t.AskSize < 0 || t.Turnover24h < 0 || t.OI < 0 {
		return errors.New("invalid ticker price/size")
	}
	return nil
}

func (t Ticker) SpreadPct() float64 {
	if t.Bid <= 0 || t.Ask <= 0 {
		return math.NaN()
	}
	return (t.Ask - t.Bid) / ((t.Ask + t.Bid) / 2) * 100
}
func (t Ticker) Fresh(now time.Time, maxAge time.Duration) bool {
	age := now.Sub(t.ObservedAt)
	return age >= 0 && age <= maxAge
}
