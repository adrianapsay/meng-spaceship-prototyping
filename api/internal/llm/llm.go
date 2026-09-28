// Package llm is a minimal, provider-neutral chat + tool-calling interface.
package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // assistant messages
	ToolCallID string     // tool-result messages

	// Raw is the provider-native assistant message. Adapters send it back
	// verbatim so provider-specific fields (e.g. Gemini thought signatures)
	// survive multi-turn tool use.
	Raw json.RawMessage
}

type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
}

// Client sends a conversation and returns the model's next assistant message.
type Client interface {
	Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error)
}
