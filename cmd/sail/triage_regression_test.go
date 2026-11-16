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

func TestCLITriageSchemaFailurePersistsAuditAndNoDecisions(t *testing.T) {
	bybit := testServer(t)
	defer bybit.Close()
	cp, _ := fixtureConfig(t, bybit.URL)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		// Use the actual snapshot ID so the failure is only the empty reason array.
		i := strings.Index(req.Messages[1].Content, "DATA_JSON:\n")
		if i < 0 {
			t.Error("missing snapshot data")
			return
		}
		var in struct {
			SnapshotID string `json:"snapshot_id"`
			Items      []struct {
				Symbol string `json:"symbol"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(req.Messages[1].Content[i+len("DATA_JSON:\n"):]), &in); err != nil {
			t.Error(err)
			return
		}
		reply, _ := json.Marshal(map[string]any{"snapshot_id": in.SnapshotID, "items": []any{map[string]any{"symbol": in.Items[0].Symbol, "side": "NONE", "decision": "REQUEST_DEEP_ANALYSIS", "reason_codes": []string{}, "explanation": "candidate"}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(reply)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 123, "completion_tokens": 45}})
	}))
	defer model.Close()
	old := makeLLMClient
	makeLLMClient = func(c config.Config) *llm.Client { return llm.New(model.URL, c.LLMModel, 3*time.Second) }
	defer func() { makeLLMClient = old }()
	command(t, "surface", "-config", cp, "-show", "0")
	err := run([]string{"triage", "-config", cp, "-limit", "1"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "symbol=BTCUSDT") || !strings.Contains(err.Error(), "field=reason_codes") {
		t.Fatalf("expected specific error, got %v", err)
	}
	report := command(t, "status", "-config", cp)
	counts := report["counts"].(map[string]any)
	if counts["llm_calls"] != float64(1) || counts["decisions"] != float64(2) || counts["virtual_positions"] != float64(0) {
		t.Fatalf("failed triage created decisions/position or audit missing: %v", counts)
	}
}
