// sail is the ONLY CLI entry point. All commands are public-data/research-only.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"time"

	"crypto-sail-ai/internal/bybit"
	"crypto-sail-ai/internal/config"
	"crypto-sail-ai/internal/decisions"
	"crypto-sail-ai/internal/features"
	"crypto-sail-ai/internal/llm"
	"crypto-sail-ai/internal/market"
	"crypto-sail-ai/internal/observer"
	"crypto-sail-ai/internal/storage"
	"crypto-sail-ai/internal/surface"
	"crypto-sail-ai/internal/watch"
)

const version = "0.1.1-research"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ОШИБКА:", err)
		os.Exit(1)
	}
}
func help(w io.Writer) {
	fmt.Fprintln(w, `Crypto Sail AI v0.1.1 — только исследование, нет реальных ордеров.
Команды:
  doctor       проверить конфигурацию и локальную БД
  collect      получить закрытые свечи BTCUSDT 1h через Bybit REST
  surface      получить весь USDT linear рынок, сохранить Surface decisions
  triage       выбрать из сохранённых Surface кандидатов и спросить Qwen
  deep         получить 5 TF для одного символа; опционально вызвать Qwen
  benchmark    проверить Qwen на синтетических сценариях БЕЗ котировок
  watch-check  проверить WAIT на таймер/зону (БЕЗ виртуального входа)
  paper        детерминированный виртуальный REPLAY по JSON со свечами
  status       счётчики базы и WAIT-кандидаты
  db-check     PRAGMA integrity_check
  backup       безопасно сделать онлайн-бэкап SQLite
Общие флаги у каждой команды: -config configs/config.example.json -db /path/to/file.db
Примеры: go run ./cmd/sail collect -symbol BTCUSDT -tf 60 -limit 100
         go run ./cmd/sail surface
         go run ./cmd/sail triage -limit 8
         go run ./cmd/sail deep -symbol BTCUSDT -side LONG -ai
         go run ./cmd/sail paper -file testdata/paper-example.json`)
}
func newFlags(name string) (*flag.FlagSet, *string, *string) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	p := f.String("config", "configs/config.example.json", "path to JSON configuration")
	db := f.String("db", "", "override database path")
	return f, p, db
}
func setup(f *flag.FlagSet, configPath, dbPath *string, args []string) (config.Config, *storage.DB, error) {
	if err := f.Parse(args); err != nil {
		return config.Config{}, nil, err
	}
	c, e := config.Load(*configPath)
	if e != nil {
		return c, nil, e
	}
	if *dbPath != "" {
		c.Database = *dbPath
	}
	db, e := storage.Open(c.Database)
	return c, db, e
}
func printing(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func snapshot(db *storage.DB, kind, symbol string, at time.Time, v any) (string, error) {
	raw, e := json.Marshal(map[string]any{"kind": kind, "symbol": symbol, "at": at, "data": v})
	if e != nil {
		return "", e
	}
	id := bybit.SnapshotID(raw)
	return id, db.SaveSnapshot(id, kind, symbol, at, v)
}
func uid() string { var b [8]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

var makeLLMClient = func(c config.Config) *llm.Client {
	return llm.New(c.LLMURL, c.LLMModel, time.Duration(c.LLMTimeoutSeconds)*time.Second)
}

func run(args []string, w io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		help(w)
		return nil
	}
	switch args[0] {
	case "doctor":
		f, cp, dbp := newFlags("doctor")
		c, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		if e = db.Integrity(); e != nil {
			return e
		}
		return printing(w, map[string]any{"version": version, "go_runtime": runtime.Version(), "paper_only": c.PaperOnly, "sqlite_integrity": "ok", "database": c.Database, "llm_endpoint": c.LLMURL, "note": "llama-server availability not checked by doctor; use triage/deep -ai"})
	case "collect":
		f, cp, dbp := newFlags("collect")
		sym := f.String("symbol", "BTCUSDT", "symbol")
		tf := f.String("tf", "60", "Bybit timeframe: 1,5,15,30,60,240,D,W")
		limit := f.Int("limit", 100, "number of closed candles (1..999)")
		c, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		if *limit > 999 || *limit < 1 {
			return errors.New("limit must be 1..999")
		}
		cs, e := bybit.New(c.BybitURL).Candles(context.Background(), *sym, *tf, *limit)
		if e != nil {
			return e
		}
		id, e := snapshot(db, "candles", *sym, time.Now().UTC(), map[string]any{"tf": *tf, "candles": cs})
		if e != nil {
			return e
		}
		return printing(w, map[string]any{"snapshot_id": id, "symbol": *sym, "tf": *tf, "count": len(cs), "first": cs[0], "last": cs[len(cs)-1]})
	case "surface":
		f, cp, dbp := newFlags("surface")
		maxPrint := f.Int("show", 10, "display first N sorted candidates")
		c, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		client := bybit.New(c.BybitURL)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		ins, e := client.Instruments(ctx)
		if e != nil {
			return e
		}
		ticks, e := client.Tickers(ctx)
		if e != nil {
			return e
		}
		tk := map[string]market.Ticker{}
		for _, v := range ticks {
			tk[v.Symbol] = v
		}
		now := time.Now().UTC()
		results := make([]surface.Result, 0, len(ins))
		runID := "surface-" + uid()
		counts := map[surface.Status]int{}
		missingTickers := 0
		for _, v := range ins {
			t, ok := tk[v.Symbol]
			if !ok {
				missingTickers++
				res := surface.Result{Symbol: v.Symbol, Status: surface.Reject, Reasons: []string{"missing_ticker"}, RunID: runID, HistoryClass: "unknown"}
				raw, e := json.Marshal(map[string]any{"at": now, "surface": res})
				if e != nil {
					return e
				}
				id := bybit.SnapshotID(raw)
				res.SnapshotID = id
				if e = db.SaveSnapshot(id, "surface", v.Symbol, now, res); e != nil {
					return e
				}
				if e = db.SaveDecision(id, "surface", v.Symbol, string(res.Status), now, res); e != nil {
					return e
				}
				counts[surface.Reject]++
				results = append(results, res)
				continue
			}
			res := surface.Evaluate(t, v, now, surface.Settings{MinTurnover: c.MinTurnover24h, MaxSpreadPct: c.MaxSpreadPct, MaxAge: c.TickerAge()})
			res.RunID = runID
			raw, e := json.Marshal(map[string]any{"at": now, "surface": res})
			if e != nil {
				return e
			}
			id := bybit.SnapshotID(raw)
			res.SnapshotID = id
			if e = db.SaveSnapshot(id, "surface", t.Symbol, now, res); e != nil {
				return e
			}
			results = append(results, res)
			counts[res.Status]++
			if e = db.SaveDecision(id, "surface", res.Symbol, string(res.Status), now, res); e != nil {
				return e
			}
		}
		sort.Slice(results, func(i, j int) bool {
			if results[i].Status != results[j].Status {
				return results[i].Status < results[j].Status
			}
			return results[i].Ticker.Turnover24h > results[j].Ticker.Turnover24h
		})
		n := *maxPrint
		if n < 0 {
			return errors.New("show must be nonnegative")
		}
		if n > len(results) {
			n = len(results)
		}
		return printing(w, map[string]any{"instruments": len(ins), "ticker_rows": len(ticks), "evaluated": len(results), "missing_tickers": missingTickers,
			"run_id": runID, "counts": counts, "top": results[:n], "note": "WATCH is also eligible for LLM triage; no nominal-price ceiling"})
	case "triage":
		f, cp, dbp := newFlags("triage")
		limit := f.Int("limit", 10, "max suggestions to send to Qwen")
		c, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		if *limit < 1 || *limit > c.MaxLLMBatch {
			return fmt.Errorf("limit must be 1..%d", c.MaxLLMBatch)
		}
		raw, e := db.Latest("surface", 1000)
		if e != nil {
			return e
		}
		var items []surface.Result
		seen := map[string]bool{}
		latestRun := ""
		for _, b := range raw {
			var s surface.Result
			if e = json.Unmarshal(b, &s); e != nil {
				return e
			}
			if latestRun == "" {
				latestRun = s.RunID
			}
			if s.RunID != latestRun {
				continue
			}
			if s.Status == surface.Reject || seen[s.Symbol] || !s.Ticker.Fresh(time.Now(), c.TickerAge()) {
				continue
			}
			seen[s.Symbol] = true
			items = append(items, s)
		}
		if len(items) == 0 {
			return errors.New("no fresh Surface items in SQLite; run surface first")
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].Ticker.Turnover24h > items[j].Ticker.Turnover24h })
		if len(items) > *limit {
			items = items[:*limit]
		}
		at := time.Now().UTC()
		id, e := snapshot(db, "triage_input", "BATCH", at, items)
		if e != nil {
			return e
		}
		out, meta, err := makeLLMClient(c).Triage(context.Background(), id, items)
		_ = db.SaveLLM(id, "triage", c.LLMModel, err == nil, int(meta.LatencyMS), meta.PromptTokens, meta.CompletionTokens, meta.Raw, errorString(err))
		if err != nil {
			return err
		}
		for _, x := range out {
			if e = db.SaveDecision(id, "triage", x.Symbol, x.Decision, at, x); e != nil {
				return e
			}
			if x.Decision == "WATCH" && x.Side != "NONE" {
				t := findResult(items, x.Symbol)
				idea := watch.Idea{Symbol: x.Symbol, Side: x.Side, SnapshotID: id, Status: "WAIT", Reason: x.ReasonCodes, NextCheck: at.Add(15 * time.Minute), Expires: at.Add(6 * time.Hour), OriginalPrice: t.Ticker.Last}
				if e = db.UpsertIdea(idea); e != nil {
					return e
				}
			}
		}
		return printing(w, map[string]any{"snapshot_id": id, "decisions": out, "latency_ms": meta.LatencyMS, "note": "REQUEST_DEEP_ANALYSIS is a research request, not an order; call deep manually at this milestone"})
	case "deep":
		f, cp, dbp := newFlags("deep")
		sym := f.String("symbol", "BTCUSDT", "USDT perpetual symbol")
		side := f.String("side", "LONG", "LONG or SHORT as hypothesis")
		ai := f.Bool("ai", false, "call local Qwen and validate result")
		c, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		if *side != "LONG" && *side != "SHORT" {
			return errors.New("side must be LONG/SHORT")
		}
		if !market.ValidSymbol(*sym) {
			return errors.New("invalid symbol")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		client := bybit.New(c.BybitURL)
		ins, e := client.Instruments(ctx)
		if e != nil {
			return e
		}
		valid := false
		var instrument market.Instrument
		for _, i := range ins {
			if i.Symbol == *sym {
				valid = true
				instrument = i
				break
			}
		}
		if !valid {
			return errors.New("instrument not tradable USDT linear perpetual")
		}
		ticks, e := client.Tickers(ctx)
		if e != nil {
			return e
		}
		var ticker market.Ticker
		found := false
		for _, t := range ticks {
			if t.Symbol == *sym {
				ticker = t
				found = true
				break
			}
		}
		if !found {
			return errors.New("missing ticker")
		}
		at := time.Now().UTC()
		sr := surface.Evaluate(ticker, instrument, at, surface.Settings{MinTurnover: c.MinTurnover24h, MaxSpreadPct: c.MaxSpreadPct, MaxAge: c.TickerAge()})
		byTF := map[string]features.Timeframe{}
		for _, tf := range []string{"5", "15", "30", "60", "240"} {
			cs, e := client.Candles(ctx, *sym, tf, 100)
			if e != nil {
				return fmt.Errorf("TF %s: %w", tf, e)
			}
			d, _ := market.IntervalDuration(tf)
			if at.Sub(cs[len(cs)-1].OpenTime.Add(d)) > 2*d {
				return fmt.Errorf("stale timeframe %s", tf)
			}
			v, e := features.Build(tf, cs)
			if e != nil {
				return e
			}
			byTF[tf] = v
		}
		pack := map[string]any{"symbol": *sym, "hypothesis_side": *side, "surface": sr, "timeframes": byTF, "collected_at": at}
		id, e := snapshot(db, "deep", *sym, at, pack)
		if e != nil {
			return e
		}
		if !*ai {
			return printing(w, map[string]any{"snapshot_id": id, "features": pack, "note": "no LLM was called; not a READY decision"})
		}
		prop, meta, err := makeLLMClient(c).Deep(ctx, id, *sym, pack)
		_ = db.SaveLLM(id, "deep", c.LLMModel, err == nil, int(meta.LatencyMS), meta.PromptTokens, meta.CompletionTokens, meta.Raw, errorString(err))
		if err != nil {
			return err
		}
		// Never let the LLM silently flip the side of a requested study.
		if prop.Side != *side && prop.Side != "NONE" {
			return errors.New("LLM changed requested research side")
		}
		status, reasons := decisions.Gate(prop, sr, byTF)
		if e = db.SaveDecision(id, "deep", *sym, string(status), at, map[string]any{"proposal": prop, "effective": status, "gate_reasons": reasons}); e != nil {
			return e
		}
		if status == decisions.Wait {
			idea := watch.Idea{Symbol: *sym, Side: *side, SnapshotID: id, Status: "WAIT", Reason: reasons, EntryLow: prop.EntryLow, EntryHigh: prop.EntryHigh, InvalidateAt: prop.InvalidateAt, OriginalPrice: ticker.Last, NextCheck: at.Add(15 * time.Minute), Expires: at.Add(6 * time.Hour)}
			if e = db.UpsertIdea(idea); e != nil {
				return e
			}
		}
		return printing(w, map[string]any{"snapshot_id": id, "LLM": prop, "effective_decision": status, "gate_reasons": reasons, "note": "READY is for research only; no orders or positions created"})
	case "benchmark":
		f, cp, dbp := newFlags("benchmark")
		file := f.String("file", "testdata/llm-benchmark.json", "JSON benchmark corpus")
		limit := f.Int("limit", 6, "number of cases to evaluate, at most 20")
		c, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		if *limit < 1 || *limit > 20 {
			return errors.New("benchmark limit must be 1..20")
		}
		data, e := os.ReadFile(*file)
		if e != nil {
			return e
		}
		var cases []struct {
			ID       string               `json:"id"`
			Symbol   string               `json:"symbol"`
			Accept   []decisions.Decision `json:"acceptable_decisions"`
			Features json.RawMessage      `json:"features"`
		}
		if e = json.Unmarshal(data, &cases); e != nil {
			return e
		}
		if len(cases) == 0 {
			return errors.New("empty benchmark")
		}
		if len(cases) > *limit {
			cases = cases[:*limit]
		}
		client := makeLLMClient(c)
		out := make([]map[string]any, 0, len(cases))
		validCount, agreement := 0, 0
		for _, tc := range cases {
			if tc.ID == "" || !market.ValidSymbol(tc.Symbol) || len(tc.Features) == 0 || len(tc.Accept) == 0 {
				return errors.New("malformed benchmark case")
			}
			sum := sha256.Sum256(append([]byte(tc.ID+tc.Symbol), tc.Features...))
			sid := fmt.Sprintf("%x", sum[:12])
			p, meta, err := client.Deep(context.Background(), sid, tc.Symbol, tc.Features)
			_ = db.SaveLLM(sid, "benchmark", c.LLMModel, err == nil, int(meta.LatencyMS), meta.PromptTokens, meta.CompletionTokens, meta.Raw, errorString(err))
			result := map[string]any{"case": tc.ID, "format_valid": err == nil, "latency_ms": meta.LatencyMS, "prompt_tokens": meta.PromptTokens, "completion_tokens": meta.CompletionTokens}
			if err != nil {
				result["error"] = err.Error()
			} else {
				validCount++
				result["decision"] = p.Decision
				result["explanation"] = p.Explanation
				match := false
				for _, d := range tc.Accept {
					if d == p.Decision {
						match = true
						break
					}
				}
				if match {
					agreement++
				}
				result["within_illustrative_labels"] = match
			}
			out = append(out, result)
		}
		return printing(w, map[string]any{"results": out, "valid_count": validCount, "illustrative_label_agreement_count": agreement, "total": len(cases), "warning": "illustrative scenario agreement is NOT strategy accuracy or probability of profit; no live market or orders"})
	case "watch-check":
		f, cp, dbp := newFlags("watch-check")
		c, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		ideas, e := db.Ideas()
		if e != nil {
			return e
		}
		if len(ideas) == 0 {
			return printing(w, map[string]any{"ideas": 0, "triggers": []any{}})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tickers, e := bybit.New(c.BybitURL).Tickers(ctx)
		if e != nil {
			return e
		}
		prices := map[string]market.Ticker{}
		for _, t := range tickers {
			prices[t.Symbol] = t
		}
		var reports []map[string]any
		now := time.Now().UTC()
		for _, idea := range ideas {
			t, ok := prices[idea.Symbol]
			if !ok || t.Validate() != nil || !t.Fresh(now, c.TickerAge()) {
				reports = append(reports, map[string]any{"symbol": idea.Symbol, "trigger": false, "reason": "missing_or_stale_ticker"})
				continue
			}
			yes, reason := idea.ShouldRecheck(now, t.Last)
			reports = append(reports, map[string]any{"symbol": idea.Symbol, "trigger": yes, "reason": reason, "price": t.Last})
		}
		return printing(w, map[string]any{"items": reports, "note": "trigger means DEEP RECHECK ONLY; current CLI does not automatically create positions"})
	case "paper":
		f, cp, dbp := newFlags("paper")
		path := f.String("file", "testdata/paper-example.json", "offline, closed OHLC bars")
		_, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		b, e := os.ReadFile(*path)
		if e != nil {
			return e
		}
		var fx struct {
			Symbol  string          `json:"symbol"`
			Side    string          `json:"side"`
			EntryAt time.Time       `json:"entry_at"`
			Entry   float64         `json:"entry"`
			Stop    float64         `json:"stop"`
			Take    float64         `json:"take"`
			Qty     float64         `json:"qty"`
			ATR     float64         `json:"atr"`
			Bars    []market.Candle `json:"bars"`
		}
		if e = json.Unmarshal(b, &fx); e != nil {
			return e
		}
		if len(fx.Bars) == 0 {
			return errors.New("fixture has no bars")
		}
		p, e := observer.NewFilled("paper-"+uid(), fx.Symbol, fx.Side, fx.EntryAt, fx.Entry, fx.Stop, fx.Take, fx.Qty, observer.DefaultRules())
		if e != nil {
			return e
		}
		// Persist the full replay inputs so an external researcher can reproduce the result.
		if len(fx.Bars) == 0 {
			return errors.New("empty paper history")
		}
		for i, bar := range fx.Bars {
			if err := bar.Validate(); err != nil {
				return err
			}
			if !bar.OpenTime.After(fx.EntryAt) || i > 0 && !bar.OpenTime.After(fx.Bars[i-1].OpenTime) {
				return errors.New("paper fixture contains non-increasing or entry bar")
			}
		}
		sourceID, e := snapshot(db, "paper_input", fx.Symbol, time.Now().UTC(), fx)
		if e != nil {
			return e
		}
		p.SourceSnapshotID = sourceID
		for _, bar := range fx.Bars {
			if p.Status != "OPEN" {
				break
			}
			if e = p.OnClosedBar(bar, fx.ATR); e != nil {
				return e
			}
		}
		if e = db.SavePosition(p.ID, p); e != nil {
			return e
		}
		for _, event := range p.Events {
			if e = db.SavePositionEvent(p.ID, event.At, event); e != nil {
				return e
			}
		}
		return printing(w, map[string]any{"position": p, "warning": "explicit assumed entry fill; OHLC stop-first approximation; not live paper trading or execution proof"})
	case "status":
		f, cp, dbp := newFlags("status")
		_, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		counts, e := db.Counts()
		if e != nil {
			return e
		}
		ideas, e := db.Ideas()
		if e != nil {
			return e
		}
		return printing(w, map[string]any{"counts": counts, "wait_ideas": ideas})
	case "db-check":
		f, cp, dbp := newFlags("db-check")
		_, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		if e = db.Integrity(); e != nil {
			return e
		}
		return printing(w, map[string]string{"sqlite_integrity": "ok"})
	case "backup":
		f, cp, dbp := newFlags("backup")
		out := f.String("out", "", "new .db backup file (must not exist)")
		_, db, e := setup(f, cp, dbp, args[1:])
		if e != nil {
			return e
		}
		defer db.Close()
		if *out == "" {
			return errors.New("-out is required")
		}
		if e = db.Backup(*out); e != nil {
			return e
		}
		return printing(w, map[string]string{"backup": *out, "status": "ok"})
	default:
		return fmt.Errorf("unknown command %q: use help", args[0])
	}
}
func errorString(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}
func findResult(items []surface.Result, symbol string) surface.Result {
	for _, x := range items {
		if x.Symbol == symbol {
			return x
		}
	}
	return surface.Result{}
}
