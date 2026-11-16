// Package config owns safe research-only configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type Config struct {
	BybitURL            string  `json:"bybit_url"`
	LLMURL              string  `json:"llm_url"`
	LLMModel            string  `json:"llm_model"`
	Database            string  `json:"database"`
	MaxTickerAgeSeconds int     `json:"max_ticker_age_seconds"`
	MinTurnover24h      float64 `json:"min_turnover_24h"`
	MaxSpreadPct        float64 `json:"max_spread_pct"`
	LLMTimeoutSeconds   int     `json:"llm_timeout_seconds"`
	MaxLLMBatch         int     `json:"max_llm_batch"`
	PaperOnly           bool    `json:"paper_only"`
}

func Default() Config {
	return Config{BybitURL: "https://api.bybit.com", LLMURL: "http://127.0.0.1:8080", LLMModel: "Qwen/Qwen3-8B-GGUF:Q4_K_M", Database: "data/crypto-sail.db", MaxTickerAgeSeconds: 180, MinTurnover24h: 100000, MaxSpreadPct: 0.5, LLMTimeoutSeconds: 120, MaxLLMBatch: 10, PaperOnly: true}
}
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return c, e
		}
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.DisallowUnknownFields()
		if e = d.Decode(&c); e != nil {
			return c, e
		}
		var extra any
		if e = d.Decode(&extra); e != io.EOF {
			return c, errors.New("config has trailing JSON")
		}
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if !c.PaperOnly {
		return errors.New("paper_only must be true: real trading is not implemented")
	}
	if c.BybitURL == "" || c.LLMURL != "http://127.0.0.1:8080" || c.Database == "" || c.LLMModel == "" {
		return errors.New("bad base URLs, db path or model; LLM must remain on localhost:8080")
	}
	if c.MaxTickerAgeSeconds <= 0 || c.MaxTickerAgeSeconds > 3600 || c.MinTurnover24h < 0 || c.MaxSpreadPct <= 0 || c.MaxSpreadPct > 10 || c.LLMTimeoutSeconds < 1 || c.LLMTimeoutSeconds > 600 || c.MaxLLMBatch < 1 || c.MaxLLMBatch > 30 {
		return fmt.Errorf("invalid numeric configuration")
	}
	return nil
}
func (c Config) TickerAge() time.Duration { return time.Duration(c.MaxTickerAgeSeconds) * time.Second }
