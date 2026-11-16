// Package llm provides a direct tool-free localhost llama.cpp Chat Completions client.
// A JSON-looking response is not trusted until all application-level checks pass.
package llm

import (
	"bytes"
	"context"
	"crypto-sail-ai/internal/decisions"
	"crypto-sail-ai/internal/surface"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	URL   string
	Model string
	HTTP  *http.Client
}

func New(url, model string, timeout time.Duration) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), Model: model, HTTP: &http.Client{Timeout: timeout}}
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
	} `json:"usage"`
}
type Result struct {
	Raw              string `json:"raw"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	LatencyMS        int64  `json:"latency_ms"`
}

func (c *Client) Complete(ctx context.Context, task string, input any, schema map[string]any) (Result, error) {
	endpoint, parseErr := url.Parse(c.URL)
	if parseErr != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.User != nil {
		return Result{}, errors.New("LLM may only be accessed on 127.0.0.1:8080")
	}
	b, e := json.Marshal(input)
	if e != nil {
		return Result{}, e
	}
	if len(b) > 120000 {
		return Result{}, errors.New("LLM input exceeds safety cap")
	}
	reqBody := map[string]any{"model": c.Model, "messages": []map[string]string{{"role": "system", "content": "Ты аналитик-классификатор исследовательской системы Bybit USDT perpetual. Используй только переданные данные, не придумывай факты, не выдавай торговые ордера. Если данных недостаточно, укажи это. Не считай confidence вероятностью прибыли. Возвращай только JSON по схеме."}, {"role": "user", "content": task + "\nDATA_JSON:\n" + string(b)}}, "temperature": 0.1, "max_tokens": 1400, "stream": false, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "market_assessment", "strict": true, "schema": schema}}}
	raw, e := json.Marshal(reqBody)
	if e != nil {
		return Result{}, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/chat/completions", bytes.NewReader(raw))
	if e != nil {
		return Result{}, e
	}
	req.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 120 * time.Second}
	}
	start := time.Now()
	resp, e := h.Do(req)
	if e != nil {
		return Result{}, e
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return Result{}, e
	}
	if resp.StatusCode != 200 {
		return Result{}, fmt.Errorf("llama-server HTTP %d", resp.StatusCode)
	}
	var rr response
	if e := json.Unmarshal(data, &rr); e != nil {
		return Result{}, e
	}
	if len(rr.Choices) != 1 {
		return Result{}, fmt.Errorf("unusable LLM choices=%d", len(rr.Choices))
	}
	content := strings.TrimSpace(rr.Choices[0].Message.Content)
	// Save the rejected answer for llm_calls diagnostics; an error is never a decision.
	result := Result{Raw: content, PromptTokens: rr.Usage.Prompt, CompletionTokens: rr.Usage.Completion, LatencyMS: time.Since(start).Milliseconds()}
	if rr.Choices[0].FinishReason != "stop" {
		return result, fmt.Errorf("unusable LLM finish_reason=%q", rr.Choices[0].FinishReason)
	}
	if content == "" || !json.Valid([]byte(content)) {
		return result, errors.New("LLM response is not JSON")
	}
	if err := ValidateJSONSchema([]byte(content), schema); err != nil {
		return result, fmt.Errorf("LLM violated response schema: %w", err)
	}
	return result, nil
}

type TriageItem struct {
	Symbol      string   `json:"symbol"`
	Side        string   `json:"side"`
	Decision    string   `json:"decision"`
	ReasonCodes []string `json:"reason_codes"`
	Explanation string   `json:"explanation"`
}
type triageResponse struct {
	SnapshotID string       `json:"snapshot_id"`
	Items      []TriageItem `json:"items"`
}

func triageSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"snapshot_id", "items"}, "properties": map[string]any{"snapshot_id": map[string]any{"type": "string"}, "items": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"symbol", "side", "decision", "reason_codes", "explanation"}, "properties": map[string]any{"symbol": map[string]any{"type": "string"}, "side": map[string]any{"type": "string", "enum": []string{"LONG", "SHORT", "NONE"}}, "decision": map[string]any{"type": "string", "enum": []string{"REQUEST_DEEP_ANALYSIS", "WATCH", "REJECT"}}, "reason_codes": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}, "explanation": map[string]any{"type": "string"}}}}}}
}

// Triage checks identity and a one-to-one mapping. Caller must cap batches and persist inputs.
func (c *Client) Triage(ctx context.Context, snapshotID string, items []surface.Result) ([]TriageItem, Result, error) {
	if len(items) == 0 || len(items) > 30 {
		return nil, Result{}, errors.New("triage batch must contain 1..30 items")
	}
	r, e := c.Complete(ctx, `Оцени необходимость Deep Screener для каждого элемента без исполнения сделок.
Повторение статуса CANDIDATE или WATCH не является достаточным обоснованием.
Для КАЖДОГО инструмента верни минимум один непустой reason_codes — краткий snake_case код
на основе переданных данных, например sufficient_turnover, tight_spread, wide_spread,
low_turnover, missing_structure, insufficient_data. Не копируй пример без подтверждения.
В explanation укажи конкретные доступные факты (turnover_24h, spread_pct, возраст данных,
историю инструмента), ограничения и причину запрашивать или не запрашивать Deep.
Отсутствующий тренд/RSI/EMA/уровни не выдумывай. Если данных для LONG/SHORT нет — side=NONE.
REQUEST_DEEP_ANALYSIS — запрос исследования, НЕ сигнал открыть позицию.
Для входного WATCH также допустим REQUEST_DEEP_ANALYSIS при обосновании данными.`, map[string]any{"snapshot_id": snapshotID, "items": items}, triageSchema())
	if e != nil {
		return nil, r, describeTriageError(r.Raw, e)
	}
	var out triageResponse
	if e := decodeStrict(r.Raw, &out); e != nil {
		return nil, r, e
	}
	if out.Items == nil || out.SnapshotID != snapshotID || len(out.Items) != len(items) {
		return nil, r, errors.New("triage snapshot/count mismatch")
	}
	expected := map[string]bool{}
	for _, x := range items {
		if expected[x.Symbol] {
			return nil, r, errors.New("duplicate triage request")
		}
		expected[x.Symbol] = true
	}
	seen := map[string]bool{}
	for _, it := range out.Items {
		if !expected[it.Symbol] || seen[it.Symbol] {
			return nil, r, fmt.Errorf("unknown/duplicate triage symbol %s", it.Symbol)
		}
		seen[it.Symbol] = true
		if it.Side != "LONG" && it.Side != "SHORT" && it.Side != "NONE" {
			return nil, r, fmt.Errorf("triage symbol=%s field=side: invalid value %q", it.Symbol, it.Side)
		}
		if it.Decision != "REQUEST_DEEP_ANALYSIS" && it.Decision != "WATCH" && it.Decision != "REJECT" {
			return nil, r, fmt.Errorf("triage symbol=%s field=decision: invalid value %q", it.Symbol, it.Decision)
		}
		if len(it.ReasonCodes) == 0 {
			return nil, r, fmt.Errorf("triage symbol=%s field=reason_codes: empty array", it.Symbol)
		}
		for _, code := range it.ReasonCodes {
			if strings.TrimSpace(code) == "" {
				return nil, r, fmt.Errorf("triage symbol=%s field=reason_codes: blank code", it.Symbol)
			}
		}
		if strings.TrimSpace(it.Explanation) == "" || len(it.Explanation) > 2000 {
			return nil, r, fmt.Errorf("triage symbol=%s field=explanation: blank or exceeds 2000 bytes", it.Symbol)
		}
	}
	return out.Items, r, nil
}

// Diagnostic only. Malformed model output is never accepted as a decision.
func describeTriageError(raw string, cause error) error {
	var response triageResponse
	if json.Unmarshal([]byte(raw), &response) == nil {
		for i, item := range response.Items {
			if len(item.ReasonCodes) == 0 {
				return fmt.Errorf("triage item=%d symbol=%s field=reason_codes: empty array: %w", i, item.Symbol, cause)
			}
		}
	}
	return fmt.Errorf("triage rejected response: %w", cause)
}
func deepSchema() map[string]any {
	fields := map[string]any{"snapshot_id": map[string]any{"type": "string"}, "symbol": map[string]any{"type": "string"}, "side": map[string]any{"type": "string", "enum": []string{"LONG", "SHORT", "NONE"}}, "decision": map[string]any{"type": "string", "enum": []string{"READY", "WAIT", "REJECT", "NEED_DATA"}}, "reason_codes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "risks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "entry_low": map[string]any{"type": "number"}, "entry_high": map[string]any{"type": "number"}, "invalidate_at": map[string]any{"type": "number"}, "explanation": map[string]any{"type": "string"}}
	required := []string{"snapshot_id", "symbol", "side", "decision", "reason_codes", "evidence", "risks", "entry_low", "entry_high", "invalidate_at", "explanation"}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": fields, "required": required}
}
func (c *Client) Deep(ctx context.Context, snapshotID, symbol string, features any) (decisions.Proposal, Result, error) {
	r, e := c.Complete(ctx, "Раздельно оцени направление и качество входа. READY только при проверенных данных; для неопределённости WAIT или NEED_DATA. entry_low/high, invalidate_at укажи 0 при отсутствии надёжной зоны. Все значения только из DATA_JSON.", map[string]any{"snapshot_id": snapshotID, "symbol": symbol, "features": features}, deepSchema())
	if e != nil {
		return decisions.Proposal{}, r, e
	}
	var p decisions.Proposal
	if e := decodeStrict(r.Raw, &p); e != nil {
		return p, r, e
	}
	if p.Evidence == nil || p.Risks == nil || p.Reasons == nil {
		return p, r, errors.New("LLM missing required arrays")
	}
	if e := p.Validate(snapshotID, symbol); e != nil {
		return p, r, e
	}
	return p, r, nil
}

// decodeStrict rejects undeclared fields and trailing JSON in addition to json.Valid.
func decodeStrict(raw string, v any) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var trailing any
	if e := d.Decode(&trailing); e != io.EOF {
		if e == nil {
			return errors.New("trailing JSON value")
		}
		return e
	}
	return nil
}
