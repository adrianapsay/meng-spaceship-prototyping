package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAICompatible speaks the Chat Completions API, which OpenAI, Gemini,
// OpenRouter, Ollama and most hosted open-weight providers implement.
type OpenAICompatible struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	MaxRetries int
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    *string       `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaiResponse struct {
	Choices []struct {
		Message json.RawMessage `json:"message"`
	} `json:"choices"`
}

func (c *OpenAICompatible) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	wire := make([]json.RawMessage, 0, len(messages))
	for _, m := range messages {
		raw, err := encodeMessage(m)
		if err != nil {
			return Message{}, err
		}
		wire = append(wire, raw)
	}
	wireTools := make([]oaiTool, len(tools))
	for i, t := range tools {
		wireTools[i].Type = "function"
		wireTools[i].Function.Name = t.Name
		wireTools[i].Function.Description = t.Description
		wireTools[i].Function.Parameters = t.Parameters
	}
	body, err := json.Marshal(map[string]any{
		"model":    c.Model,
		"messages": wire,
		"tools":    wireTools,
	})
	if err != nil {
		return Message{}, err
	}

	respBody, err := c.post(ctx, body)
	if err != nil {
		return Message{}, err
	}
	var resp oaiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return Message{}, fmt.Errorf("decode chat response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Message{}, fmt.Errorf("chat response has no choices: %s", truncate(respBody, 300))
	}
	return decodeAssistant(resp.Choices[0].Message)
}

func (c *OpenAICompatible) post(ctx context.Context, body []byte) ([]byte, error) {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	url := strings.TrimSuffix(c.BaseURL, "/") + "/chat/completions"

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("chat request: %w", err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		if res.StatusCode == http.StatusOK {
			return data, nil
		}
		msg := errorMessage(data)
		// A daily quota will not recover within any sensible retry window.
		dailyQuota := res.StatusCode == http.StatusTooManyRequests && strings.Contains(string(data), "PerDay")
		retryable := (res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500) && !dailyQuota
		if !retryable || attempt >= c.MaxRetries {
			return nil, fmt.Errorf("%s: %s", res.Status, msg)
		}
		wait := backoff(attempt, res.Header.Get("Retry-After"))
		notifyRetry(ctx, attempt+1, wait, res.Status)
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// errorMessage extracts the human-readable message from an OpenAI-style error
// body ({"error":{"message":...}}, or Gemini's list-wrapped variant).
func errorMessage(body []byte) string {
	type apiError struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var single apiError
	if json.Unmarshal(body, &single) == nil && single.Error.Message != "" {
		return single.Error.Message
	}
	var list []apiError
	if json.Unmarshal(body, &list) == nil && len(list) > 0 && list[0].Error.Message != "" {
		return list[0].Error.Message
	}
	return truncate(body, 500)
}

func encodeMessage(m Message) (json.RawMessage, error) {
	if m.Role == RoleAssistant && m.Raw != nil {
		return m.Raw, nil
	}
	w := oaiMessage{Role: string(m.Role), Content: &m.Content, ToolCallID: m.ToolCallID}
	for _, tc := range m.ToolCalls {
		var call oaiToolCall
		call.ID, call.Type = tc.ID, "function"
		call.Function.Name, call.Function.Arguments = tc.Name, string(tc.Arguments)
		w.ToolCalls = append(w.ToolCalls, call)
	}
	return json.Marshal(w)
}

func decodeAssistant(raw json.RawMessage) (Message, error) {
	var w oaiMessage
	if err := json.Unmarshal(raw, &w); err != nil {
		return Message{}, fmt.Errorf("decode assistant message: %w", err)
	}
	m := Message{Role: RoleAssistant, Raw: raw}
	if w.Content != nil {
		m.Content = *w.Content
	}
	for _, tc := range w.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(args) {
			// Keep malformed arguments as a JSON string so the agent can report it.
			args, _ = json.Marshal(tc.Function.Arguments)
		}
		m.ToolCalls = append(m.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args})
	}
	return m, nil
}

func backoff(attempt int, retryAfter string) time.Duration {
	if s, err := strconv.Atoi(retryAfter); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return min(time.Duration(1<<attempt)*2*time.Second, 30*time.Second)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

var ErrUnknownProvider = errors.New("unknown or unconfigured provider")
