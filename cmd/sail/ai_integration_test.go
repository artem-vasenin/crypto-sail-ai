package main

import (
	"bytes"
	"crypto-sail-ai/internal/config"
	"crypto-sail-ai/internal/llm"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCLITriageDeepWaitBenchmarkWithFakeQwen(t *testing.T) {
	bybitServer := testServer(t)
	defer bybitServer.Close()
	cp, _ := fixtureConfig(t, bybitServer.URL)
	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools          any `json:"tools"`
			ResponseFormat any `json:"response_format"`
		}
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
			return
		}
		if req.Tools != nil || len(req.Messages) != 2 || req.ResponseFormat == nil {
			t.Error("bad chat request")
		}
		user := req.Messages[1].Content
		idx := strings.Index(user, "DATA_JSON:\n")
		if idx < 0 {
			t.Error("missing DATA_JSON")
			return
		}
		var in struct {
			SnapshotID string `json:"snapshot_id"`
			Symbol     string `json:"symbol"`
			Items      []struct {
				Symbol string `json:"symbol"`
			} `json:"items"`
		}
		if e := json.Unmarshal([]byte(user[idx+len("DATA_JSON:\n"):]), &in); e != nil {
			t.Error(e)
			return
		}
		var answer string
		if len(in.Items) > 0 {
			items := make([]map[string]any, 0, len(in.Items))
			for _, it := range in.Items {
				items = append(items, map[string]any{"symbol": it.Symbol, "side": "LONG", "decision": "WATCH", "reason_codes": []string{"needs_new_snapshot"}, "explanation": "Please re-evaluate later"})
			}
			raw, _ := json.Marshal(map[string]any{"snapshot_id": in.SnapshotID, "items": items})
			answer = string(raw)
		} else {
			if in.Symbol == "" {
				t.Error("missing deep symbol")
				return
			}
			raw, _ := json.Marshal(map[string]any{"snapshot_id": in.SnapshotID, "symbol": in.Symbol, "side": "LONG", "decision": "WAIT", "reason_codes": []string{"entry_quality_uncertain"}, "evidence": []string{"synthetic test"}, "risks": []string{"unknown confirmation"}, "entry_low": 95, "entry_high": 98, "invalidate_at": 90, "explanation": "Further evidence needed"})
			answer = string(raw)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 40}})
	}))
	defer aiServer.Close()
	oldFactory := makeLLMClient
	makeLLMClient = func(c config.Config) *llm.Client { return llm.New(aiServer.URL, c.LLMModel, 5*time.Second) }
	defer func() { makeLLMClient = oldFactory }()
	command(t, "surface", "-config", cp, "-show", "0")
	triage := command(t, "triage", "-config", cp, "-limit", "1")
	if len(triage["decisions"].([]any)) != 1 {
		t.Fatal(triage)
	}
	deep := command(t, "deep", "-config", cp, "-symbol", "BTCUSDT", "-side", "LONG", "-ai")
	if deep["effective_decision"] != "WAIT" {
		t.Fatal(deep)
	}
	status := command(t, "status", "-config", cp)
	if len(status["wait_ideas"].([]any)) != 1 {
		t.Fatal(status)
	}
	report := command(t, "watch-check", "-config", cp)
	if _, ok := report["items"]; !ok {
		t.Fatal(report)
	}
	// Verify no automatic position was opened by WATCH/WAIT or LLM.
	counts := status["counts"].(map[string]any)
	if counts["virtual_positions"] != float64(0) || counts["llm_calls"] != float64(2) {
		t.Fatal(counts)
	}
	benchmarkFile := "../../testdata/llm-benchmark.json"
	bench := command(t, "benchmark", "-config", cp, "-file", benchmarkFile, "-limit", "2")
	if bench["valid_count"] != float64(2) {
		t.Fatal(bench)
	}
	if e := run([]string{"triage", "-config", cp, "-limit", "200"}, &bytes.Buffer{}); e == nil {
		t.Fatal("unbounded LLM batch accepted")
	}
}
