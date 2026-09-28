package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleToolRoundTrip(t *testing.T) {
	var calls int
	var lastBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &lastBody)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"c1","type":"function","function":{"name":"build","arguments":"{\"x\":1}"}}],
			"extra_content":{"google":{"thought_signature":"sig"}}}}]}`)
	}))
	defer srv.Close()

	c := &OpenAICompatible{BaseURL: srv.URL, APIKey: "k", Model: "m", MaxRetries: 1}
	tools := []Tool{{Name: "build", Parameters: json.RawMessage(`{"type":"object"}`)}}
	reply, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected one retry after 429, got %d calls", calls)
	}
	if len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Name != "build" || string(reply.ToolCalls[0].Arguments) != `{"x":1}` {
		t.Fatalf("tool call not decoded: %+v", reply.ToolCalls)
	}
	if lastBody["model"] != "m" || len(lastBody["tools"].([]any)) != 1 {
		t.Fatalf("request body %v", lastBody)
	}

	// Provider-specific fields must be echoed back on the next turn.
	raw, err := encodeMessage(reply)
	if err != nil || !strings.Contains(string(raw), "thought_signature") {
		t.Fatalf("raw assistant message not preserved: %s", raw)
	}
}

func TestMalformedArgumentsBecomeJSONString(t *testing.T) {
	m, err := decodeAssistant(json.RawMessage(`{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"f","arguments":"{oops"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(m.ToolCalls[0].Arguments); got != `"{oops"` {
		t.Fatalf("got %s", got)
	}
}

func TestDailyQuotaFailsFastWithReadableMessage(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `[{"error":{"code":429,"message":"Quota exceeded: limit 20","status":"RESOURCE_EXHAUSTED",
			"details":[{"violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]}]}}]`)
	}))
	defer srv.Close()

	var retries int
	ctx := WithRetryHook(context.Background(), func(RetryInfo) { retries++ })
	c := &OpenAICompatible{BaseURL: srv.URL, Model: "m", MaxRetries: 5}
	_, err := c.Chat(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err == nil || calls != 1 || retries != 0 {
		t.Fatalf("err %v, calls %d, retries %d", err, calls, retries)
	}
	if got := err.Error(); got != "429 Too Many Requests: Quota exceeded: limit 20" {
		t.Fatalf("got %q", got)
	}
}
