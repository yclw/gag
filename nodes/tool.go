package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"gag/graph"
)

type Tool interface {
	Name() string
	Definition() ToolDefinition
	Execute(context.Context, ToolCall, *[]graph.Event, graph.EmitFunc) (ToolResult, error)
}

type ToolResult struct {
	Content  []ContentPart   `json:"content,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type ToolNode struct {
	NodeID string
	Tools  map[string]Tool
}

func NewToolNode(id string, tools ...Tool) (*ToolNode, error) {
	registry := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		registry[tool.Name()] = tool
	}
	return &ToolNode{NodeID: id, Tools: registry}, nil
}

var _ graph.Node = (*ToolNode)(nil)

func (n *ToolNode) ID() string {
	return n.NodeID
}

func (n *ToolNode) Run(ctx context.Context, events *[]graph.Event, emit graph.EmitFunc) (graph.NodeResult, error) {
	calls, err := UnfinishedToolCalls(*events)
	if err != nil {
		return graph.NodeResult{}, err
	}

	for _, call := range calls {
		var result ToolResult
		if tool := n.Tools[call.ToolName]; tool == nil {
			result = toolErrorResult(fmt.Errorf("tool %q not found", call.ToolName))
		} else {
			result, err = tool.Execute(ctx, call, events, emit)
			if action, ok := graph.ControlAction(err); ok {
				return graph.NodeResult{Action: action}, nil
			}
			if err != nil {
				result = toolErrorResult(err)
			}
		}

		if err := appendToolResult(ctx, events, emit, call, result); err != nil {
			return graph.NodeResult{}, err
		}
	}
	return graph.NodeResult{Action: graph.RunContinue}, nil
}

func toolErrorResult(err error) ToolResult {
	return ToolResult{Content: []ContentPart{{Type: "text", Text: err.Error()}}}
}

func appendToolResult(ctx context.Context, events *[]graph.Event, emit graph.EmitFunc, call ToolCall, result ToolResult) error {
	event, err := graph.NewEvent(ModelMessageEventType, ModelMessage{
		Role:       ToolRole,
		Content:    result.Content,
		Name:       call.ToolName,
		ToolCallID: call.ID,
		Metadata:   result.Metadata,
	})
	if err != nil {
		return err
	}

	*events = append(*events, event)
	if emit == nil {
		return nil
	}
	return emit(ctx, event)
}

func UnfinishedToolCalls(events []graph.Event) ([]ToolCall, error) {
	completed := make(map[string]bool)
	var calls []ToolCall
	for _, event := range events {
		if event.Type != ModelMessageEventType {
			continue
		}

		message, err := graph.DecodeEvent[ModelMessage](event, ModelMessageEventType)
		if err != nil {
			return nil, err
		}

		if message.Role == ToolRole {
			completed[message.ToolCallID] = true
		}

		for _, call := range message.ToolCalls {
			calls = append(calls, call)
		}
	}

	unfinished := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		if !completed[call.ID] {
			unfinished = append(unfinished, call)
		}
	}
	return unfinished, nil
}
