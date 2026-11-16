package llm

import (
	"context"
	"crypto-sail-ai/internal/surface"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Reproduces the five-empty-reasons failure observed with the local Qwen3-8B.
func TestTriageFiveEmptyReasonsRejectAndKeepRaw(t *testing.T) {
	symbols := []string{"BTCUSDT", "ETHUSDT", "SOXLUSDT", "SOLUSDT", "XRPUSDT"}
	payload := triageResponse{SnapshotID: "research-snapshot"}
	inputs := make([]surface.Result, 0, len(symbols))
	for _, symbol := range symbols {
		inputs = append(inputs, surface.Result{Symbol: symbol})
		payload.Items = append(payload.Items, TriageItem{Symbol: symbol, Side: "NONE", Decision: "REQUEST_DEEP_ANALYSIS", ReasonCodes: []string{}, Explanation: "It was CANDIDATE"})
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(bytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ResponseFormat struct {
				JSONSchema struct {
					Schema map[string]any `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		reasons := request.ResponseFormat.JSONSchema.Schema["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["reason_codes"].(map[string]any)
		if reasons["minItems"] != float64(1) {
			t.Errorf("missing minItems=1 in the schema: %v", reasons)
		}
		if len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, "turnover_24h") {
			t.Error("triage prompt lacks source-based justification instructions")
		}
		chat(w, raw, "stop")
	}))
	defer server.Close()
	results, meta, err := New(server.URL, "qwen", 2*time.Second).Triage(context.Background(), "research-snapshot", inputs)
	if err == nil || !strings.Contains(err.Error(), "symbol=BTCUSDT") || !strings.Contains(err.Error(), "field=reason_codes") {
		t.Fatalf("want specific rejection, got %v", err)
	}
	if len(results) != 0 || meta.Raw != raw || meta.PromptTokens != 12 || meta.CompletionTokens != 19 {
		t.Fatalf("rejected response was accepted or lost: results=%v metadata=%+v", results, meta)
	}
}

func TestTriageRejectWhitespaceReasonsAndExplanation(t *testing.T) {
	cases := []struct{ name, reasons, explanation, field string }{
		{"blank_reason", `[" "]`, `"checked"`, "reason_codes"},
		{"blank_explanation", `["sufficient_turnover"]`, `"  "`, "explanation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"NONE","decision":"WATCH","reason_codes":` + tc.reasons + `,"explanation":` + tc.explanation + `}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { chat(w, raw, "stop") }))
			defer server.Close()
			got, meta, err := New(server.URL, "qwen", time.Second).Triage(context.Background(), "sid", []surface.Result{{Symbol: "BTCUSDT"}})
			if err == nil || !strings.Contains(err.Error(), "field="+tc.field) || len(got) != 0 || meta.Raw != raw {
				t.Fatalf("should fail closed: %v %+v", err, meta)
			}
		})
	}
}

func TestTriageTruncatedJSONRetainedButNeverAccepted(t *testing.T) {
	partial := `{"snapshot_id":"sid","items":[`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { chat(w, partial, "length") }))
	defer server.Close()
	got, meta, err := New(server.URL, "qwen", time.Second).Triage(context.Background(), "sid", []surface.Result{{Symbol: "BTCUSDT"}})
	if err == nil || len(got) != 0 || meta.Raw != partial || meta.CompletionTokens != 19 {
		t.Fatalf("truncated response accepted or lost: %v %+v", err, meta)
	}
}
