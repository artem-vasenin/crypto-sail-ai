// Package features computes indicators and simple swing structure from CLOSED candles.
package features

import (
	"crypto-sail-ai/internal/market"
	"errors"
	"math"
)

type Optional struct {
	Value  float64 `json:"value"`
	Valid  bool    `json:"valid"`
	Reason string  `json:"reason,omitempty"`
}

func Missing(reason string) Optional { return Optional{Reason: reason} }
func Available(v float64) Optional   { return Optional{Value: v, Valid: true} }
func SMA(cs []market.Candle, n int) Optional {
	if n < 1 || len(cs) < n {
		return Missing("insufficient_history")
	}
	sum := 0.
	for _, c := range cs[len(cs)-n:] {
		sum += c.Close
	}
	return Available(sum / float64(n))
}
func EMA(cs []market.Candle, n int) Optional {
	if n < 1 || len(cs) < n {
		return Missing("insufficient_history")
	}
	k := 2. / float64(n+1)
	v := 0.
	for _, c := range cs[:n] {
		v += c.Close
	}
	v /= float64(n)
	for _, c := range cs[n:] {
		v = k*c.Close + (1-k)*v
	}
	return Available(v)
}

// RSI uses Wilder smoothing and requires n+1 closes.
func RSI(cs []market.Candle, n int) Optional {
	if n <= 0 || len(cs) < n+1 {
		return Missing("insufficient_history")
	}
	gain, loss := 0., 0.
	for i := 1; i <= n; i++ {
		delta := cs[i].Close - cs[i-1].Close
		if delta > 0 {
			gain += delta
		} else {
			loss -= delta
		}
	}
	gain /= float64(n)
	loss /= float64(n)
	for i := n + 1; i < len(cs); i++ {
		d := cs[i].Close - cs[i-1].Close
		g, l := 0., 0.
		if d > 0 {
			g = d
		} else {
			l = -d
		}
		gain = (gain*float64(n-1) + g) / float64(n)
		loss = (loss*float64(n-1) + l) / float64(n)
	}
	if loss == 0 {
		if gain == 0 {
			return Available(50)
		}
		return Available(100)
	}
	return Available(100 - 100/(1+gain/loss))
}
func ATR(cs []market.Candle, n int) Optional {
	if n <= 0 || len(cs) < n+1 {
		return Missing("insufficient_history")
	}
	trs := make([]float64, 0, len(cs)-1)
	for i := 1; i < len(cs); i++ {
		p := cs[i-1].Close
		c := cs[i]
		trs = append(trs, math.Max(c.High-c.Low, math.Max(math.Abs(c.High-p), math.Abs(c.Low-p))))
	}
	a := 0.
	for _, tr := range trs[:n] {
		a += tr
	}
	a /= float64(n)
	for _, tr := range trs[n:] {
		a = (a*float64(n-1) + tr) / float64(n)
	}
	return Available(a)
}

// Structure is UNKNOWN until at least two confirmed high pivots and low pivots exist.
type Structure struct {
	HighPattern  string   `json:"high_pattern"`
	LowPattern   string   `json:"low_pattern"`
	LastHigh     Optional `json:"last_high"`
	PreviousHigh Optional `json:"previous_high"`
	LastLow      Optional `json:"last_low"`
	PreviousLow  Optional `json:"previous_low"`
	HighCount    int      `json:"high_count"`
	LowCount     int      `json:"low_count"`
	Regime       string   `json:"regime"`
}

func Swings(cs []market.Candle, radius int) Structure {
	s := Structure{HighPattern: "UNKNOWN", LowPattern: "UNKNOWN", Regime: "UNKNOWN", LastHigh: Missing("insufficient_pivots"), PreviousHigh: Missing("insufficient_pivots"), LastLow: Missing("insufficient_pivots"), PreviousLow: Missing("insufficient_pivots")}
	if radius < 1 || len(cs) < radius*2+3 {
		return s
	}
	var highs, lows []float64
	for i := radius; i+radius < len(cs); i++ {
		isHigh, isLow := true, true
		for j := i - radius; j <= i+radius; j++ {
			if j == i {
				continue
			}
			if cs[j].High >= cs[i].High {
				isHigh = false
			}
			if cs[j].Low <= cs[i].Low {
				isLow = false
			}
		}
		if isHigh {
			highs = append(highs, cs[i].High)
		}
		if isLow {
			lows = append(lows, cs[i].Low)
		}
	}
	s.HighCount = len(highs)
	s.LowCount = len(lows)
	if len(highs) >= 2 {
		s.LastHigh = Available(highs[len(highs)-1])
		s.PreviousHigh = Available(highs[len(highs)-2])
		s.HighPattern = compare(s.LastHigh.Value, s.PreviousHigh.Value, "HH", "LH")
	}
	if len(lows) >= 2 {
		s.LastLow = Available(lows[len(lows)-1])
		s.PreviousLow = Available(lows[len(lows)-2])
		s.LowPattern = compare(s.LastLow.Value, s.PreviousLow.Value, "HL", "LL")
	}
	switch {
	case s.HighPattern == "HH" && s.LowPattern == "HL":
		s.Regime = "BULL"
	case s.HighPattern == "LH" && s.LowPattern == "LL":
		s.Regime = "BEAR"
	case s.HighPattern == "UNKNOWN" || s.LowPattern == "UNKNOWN":
		s.Regime = "UNKNOWN"
	case s.HighPattern == "EQ" || s.LowPattern == "EQ":
		s.Regime = "RANGE"
	default:
		s.Regime = "CONFLICT"
	}
	return s
}
func compare(a, b float64, higher, lower string) string {
	const eps = 1e-9
	if math.Abs(a-b) <= eps*math.Max(1, math.Abs(b)) {
		return "EQ"
	}
	if a > b {
		return higher
	}
	return lower
}

type Timeframe struct {
	Timeframe  string    `json:"timeframe"`
	Count      int       `json:"closed_candles"`
	LastPrice  float64   `json:"last_close"`
	RSI14      Optional  `json:"rsi14"`
	EMA20      Optional  `json:"ema20"`
	EMA50      Optional  `json:"ema50"`
	SMA200     Optional  `json:"sma200"`
	ATR14      Optional  `json:"atr14"`
	ATRPercent Optional  `json:"atr_pct"`
	Structure  Structure `json:"structure"`
}

func Build(tf string, cs []market.Candle) (Timeframe, error) {
	if len(cs) == 0 {
		return Timeframe{}, errors.New("empty candles")
	}
	for _, c := range cs {
		if e := c.Validate(); e != nil {
			return Timeframe{}, e
		}
	}
	f := Timeframe{Timeframe: tf, Count: len(cs), LastPrice: cs[len(cs)-1].Close, RSI14: RSI(cs, 14), EMA20: EMA(cs, 20), EMA50: EMA(cs, 50), SMA200: SMA(cs, 200), ATR14: ATR(cs, 14), Structure: Swings(cs, 2)}
	if f.ATR14.Valid {
		f.ATRPercent = Available(f.ATR14.Value / f.LastPrice * 100)
	} else {
		f.ATRPercent = Missing("insufficient_history")
	}
	return f, nil
}
