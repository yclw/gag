package nodes

import (
	"context"
	"encoding/json"

	"github.com/yclw/gag/graph"

	"github.com/google/jsonschema-go/jsonschema"
)

type Model interface {
	Generate(context.Context, ModelInput, func(ModelChunk) error) (ModelMessage, error)
}

const ModelMessageEventType string = "model.message"

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

const (
	TextContentType      string = "text"
	ImageContentType     string = "image"
	AudioContentType     string = "audio"
	VideoContentType     string = "video"
	FileContentType      string = "file"
	ReasoningContentType string = "reasoning"
	JSONContentType      string = "json"
)

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

const ModelDeltaEventType = "model.delta"

type ModelDelta struct {
	NodeID string     `json:"node_id"`
	Chunk  ModelChunk `json:"chunk"`
}

type ModelNode struct {
	NodeID       string
	Model        Model
	SystemPrompt string
	Tools        []ToolDefinition
	Metadata     json.RawMessage
}

func NewModelNode(id string, model Model) (*ModelNode, error) {
	return &ModelNode{NodeID: id, Model: model}, nil
}

var _ graph.Node = (*ModelNode)(nil)

func (n *ModelNode) ID() string {
	return n.NodeID
}

func (n *ModelNode) Run(ctx context.Context, events *[]graph.Event, emit graph.EmitFunc) (graph.NodeResult, error) {
	messages, err := modelMessages(*events)
	if err != nil {
		return graph.NodeResult{}, err
	}

	messages = append([]ModelMessage{{
		Role: SystemRole,
		Content: []ContentPart{{
			Type: TextContentType,
			Text: n.SystemPrompt,
		}},
	}}, messages...)

	partial := ModelMessage{Role: AssistantRole}
	var message ModelMessage
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	} else {
		message, err = n.Model.Generate(ctx, ModelInput{
			Messages: messages,
			Tools:    n.Tools,
			Metadata: n.Metadata,
		}, func(chunk ModelChunk) error {
			partial.Content = append(
				partial.Content,
				chunk.Message.Content...,
			)

			event, err := graph.NewEvent(
				ModelDeltaEventType,
				ModelDelta{
					NodeID: n.NodeID,
					Chunk:  chunk,
				},
			)
			if err != nil {
				return err
			}
			return emit(ctx, event)
		})
	}
	if action, ok := graph.ControlAction(err); ok {
		return graph.NodeResult{Action: action}, nil
	}

	if err != nil {
		cause := context.Cause(ctx)
		if cause == nil {
			return graph.NodeResult{}, err
		}

		text := cause.Error()
		if len(partial.Content) > 0 {
			text = "\n\n" + text
		}
		partial.Content = append(partial.Content, ContentPart{
			Type: TextContentType,
			Text: text,
		})
		partial.StopReason = "cancelled"
		message = partial
	}

	message.Content = appendContent(nil, message.Content...)

	event, err := graph.NewEvent(ModelMessageEventType, message)
	if err != nil {
		return graph.NodeResult{}, err
	}

	*events = append(*events, event)
	if err := emit(ctx, event); err != nil {
		return graph.NodeResult{}, err
	}

	return graph.NodeResult{Action: graph.RunContinue}, nil
}

func modelMessages(events []graph.Event) ([]ModelMessage, error) {
	var messages []ModelMessage
	for _, event := range events {
		if event.Type != ModelMessageEventType {
			continue
		}

		message, err := graph.DecodeEvent[ModelMessage](
			event,
			ModelMessageEventType,
		)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func appendContent(parts []ContentPart, incoming ...ContentPart) []ContentPart {
	for _, part := range incoming {
		last := len(parts) - 1
		if part.Type == TextContentType &&
			last >= 0 &&
			parts[last].Type == TextContentType &&
			len(parts[last].Metadata) == 0 &&
			len(part.Metadata) == 0 {
			parts[last].Text += part.Text
			continue
		}

		parts = append(parts, part)
	}
	return parts
}
