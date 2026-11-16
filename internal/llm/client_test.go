package llm

import (
	"context"
	"crypto-sail-ai/internal/surface"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func chat(w http.ResponseWriter, content string, finish string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 19}})
}
func TestTriageFullContract(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			t.Error("wrong method/path")
		}
		var body map[string]json.RawMessage
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if _, ok := body["tools"]; ok {
			t.Error("never offer tools")
		}
		if _, ok := body["response_format"]; !ok {
			t.Error("schema missing")
		}
		chat(w, `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"REQUEST_DEEP_ANALYSIS","reason_codes":["needs_structure"],"explanation":"Need 4H"}]}`, "stop")
	}))
	defer ts.Close()
	c := New(ts.URL, "qwen", 3*time.Second)
	items, res, e := c.Triage(context.Background(), "sid", []surface.Result{{Symbol: "BTCUSDT", Status: surface.Watch}})
	if e != nil || len(items) != 1 || res.CompletionTokens != 19 {
		t.Fatalf("%+v %+v %v", items, res, e)
	}
}
func TestLLMFailClosedMatrix(t *testing.T) {
	for _, x := range []struct{ name, content, reason string }{
		{"truncated", `{"snapshot_id":"sid","items":[]}`, "length"},
		{"bad_json", "not json", "stop"},
		{"wrong_snapshot", `{"snapshot_id":"other","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":["x"],"explanation":"x"}]}`, "stop"},
		{"wrong_symbol", `{"snapshot_id":"sid","items":[{"symbol":"ETHUSDT","side":"LONG","decision":"WATCH","reason_codes":["x"],"explanation":"x"}]}`, "stop"},
		{"empty_reasons", `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":[],"explanation":"x"}]}`, "stop"},
	} {
		t.Run(x.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { chat(w, x.content, x.reason) }))
			defer ts.Close()
			_, _, e := New(ts.URL, "qwen", time.Second).Triage(context.Background(), "sid", []surface.Result{{Symbol: "BTCUSDT"}})
			if e == nil {
				t.Fatal("untrusted answer accepted")
			}
		})
	}
}
func TestLLMHostAndHTTPFailures(t *testing.T) {
	c := New("https://evil.example", "qwen", time.Second)
	if _, e := c.Complete(context.Background(), "x", map[string]int{"a": 1}, triageSchema()); e == nil {
		t.Fatal("public host accepted")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); fmt.Fprint(w, "broken") }))
	defer ts.Close()
	c = New(ts.URL, "qwen", time.Second)
	if _, e := c.Complete(context.Background(), "x", "hello", triageSchema()); e == nil || !strings.Contains(e.Error(), "500") {
		t.Fatal(e)
	}
}
func TestDeepIdentityAndJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chat(w, `{"snapshot_id":"123","symbol":"BTCUSDT","side":"LONG","decision":"WAIT","reason_codes":["overbought"],"evidence":["RSI=80"],"risks":["near resistance"],"entry_low":99,"entry_high":100,"invalidate_at":90,"explanation":"wait for pullback"}`, "stop")
	}))
	defer ts.Close()
	p, r, e := New(ts.URL, "qwen", time.Second).Deep(context.Background(), "123", "BTCUSDT", map[string]any{"rsi": 80})
	if e != nil || p.Decision != "WAIT" || r.Raw == "" {
		t.Fatalf("%+v %v", p, e)
	}
}

func TestSchemaChecksEvenWhenServerClaimsStop(t *testing.T) {
	good := `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":["needs_confirmation"],"explanation":"review"}]}`
	cases := []struct {
		name, answer string
		valid        bool
	}{
		{"valid", good, true},
		{"unexpected_field", `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":["x"],"explanation":"x","permission":"place_order"}]}`, false},
		{"missing_required", `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":["x"]}]}`, false},
		{"invalid_enum", `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"BUY","reason_codes":["x"],"explanation":"x"}]}`, false},
		{"wrong_type", `{"snapshot_id":"sid","items":"not an array"}`, false},
		{"missing_items", `{"snapshot_id":"sid"}`, false},
		{"empty_items", `{"snapshot_id":"sid","items":[]}`, false},
		{"number_as_side", `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":1,"decision":"WATCH","reason_codes":["x"],"explanation":"x"}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateJSONSchema([]byte(tc.answer), triageSchema())
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
func TestSchemaUnsupportedConstructFailClosed(t *testing.T) {
	if err := ValidateJSONSchema([]byte(`42`), map[string]any{"type": "integer"}); err == nil {
		t.Fatal("unsupported accepted")
	}
	if err := ValidateJSONSchema([]byte(`{"x":3}`), map[string]any{"type": "object", "required": []string{"x"}, "properties": map[string]any{"x": map[string]any{"type": "number"}}, "additionalProperties": false}); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownJSONAndMissingDeepArraysRejected(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"extra_property", `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":["x"],"explanation":"x","force_order":true}]}`},
		{"trailing", `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":["x"],"explanation":"x"}]} {"more":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { chat(w, tc.content, "stop") }))
			defer ts.Close()
			_, _, e := New(ts.URL, "qwen", time.Second).Triage(context.Background(), "sid", []surface.Result{{Symbol: "BTCUSDT"}})
			if e == nil {
				t.Fatal("unexpected acceptance")
			}
		})
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chat(w, `{"snapshot_id":"s","symbol":"BTCUSDT","side":"LONG","decision":"WAIT","reason_codes":["x"],"entry_low":0,"entry_high":0,"invalidate_at":0,"explanation":"missing arrays"}`, "stop")
	}))
	defer ts.Close()
	_, _, e := New(ts.URL, "qwen", time.Second).Deep(context.Background(), "s", "BTCUSDT", map[string]string{"x": "y"})
	if e == nil {
		t.Fatal("deep missing arrays accepted")
	}
}

func TestCompleteDoesNotTrustServerSideJSONSchema(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chat(w, `{"snapshot_id":"sid","items":[{"symbol":"BTCUSDT","side":"LONG","decision":"WATCH","reason_codes":["x"],"explanation":"x","extra_field":true}]}`, "stop")
	}))
	defer ts.Close()
	_, _, err := New(ts.URL, "qwen", 2*time.Second).Triage(context.Background(), "sid", []surface.Result{{Symbol: "BTCUSDT"}})
	if err == nil || !strings.Contains(err.Error(), "response schema") {
		t.Fatalf("LLM response schema was not independently enforced: %v", err)
	}
}
