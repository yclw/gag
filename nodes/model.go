package nodes

import (
	"context"
	"encoding/json"
	"gag/graph"

	"github.com/google/jsonschema-go/jsonschema"
)

type Model interface {
	Generate(context.Context, ModelInput, func(ModelChunk) error) (ModelMessage, error)
}

const (
	SystemRole    string = "system"
	UserRole      string = "user"
	AssistantRole string = "assistant"
	ToolRole      string = "tool"
)

type ModelMessage struct {
	Role       string          `json:"role"`
	Content    []ContentPart   `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Usage      ModelUsage      `json:"usage,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type ModelInput struct {
	Messages []ModelMessage   `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Metadata json.RawMessage  `json:"metadata,omitempty"`
}

type ToolDefinition struct {
	Type        string             `json:"type,omitempty"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	InputSchema *jsonschema.Schema `json:"input_schema,omitempty"`
	Metadata    json.RawMessage    `json:"metadata,omitempty"`
}

type ContentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Media     *Media          `json:"media,omitempty"`
	Reasoning *Reasoning      `json:"reasoning,omitempty"`
	JSON      json.RawMessage `json:"json,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type Media struct {
	URL      string          `json:"url,omitempty"`
	Data     []byte          `json:"data,omitempty"`
	ID       string          `json:"id,omitempty"`
	MIMEType string          `json:"mime_type,omitempty"`
	Name     string          `json:"name,omitempty"`
	Detail   string          `json:"detail,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type Reasoning struct {
	Text      string          `json:"text,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	ToolName  string          `json:"tool_name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Index     *int            `json:"index,omitempty"`
	Type      string          `json:"type,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type ModelUsage struct {
	InputTokens       int             `json:"input_tokens,omitempty"`
	OutputTokens      int             `json:"output_tokens,omitempty"`
	TotalTokens       int             `json:"total_tokens,omitempty"`
	CachedInputTokens int             `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int             `json:"reasoning_tokens,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
}

type ModelChunk struct {
	Message    ModelMessage    `json:"message"`
	StopReason string          `json:"stop_reason,omitempty"`
	Usage      ModelUsage      `json:"usage,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type ModelNode struct {
	NodeID   string
	Model    Model
	Tools    []ToolDefinition
	Metadata json.RawMessage
}

var _ graph.Node = (*ModelNode)(nil)

func (n *ModelNode) ID() string {
	return n.NodeID
}

func (n *ModelNode) Run(ctx context.Context, events *[]graph.Event, emit graph.EmitFunc) (graph.NodeResult, error) {
	// TODO
	panic("")
}
