package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/yclw/gag/graph"
	"github.com/yclw/gag/nodes"
	"github.com/yclw/gag/nodes/interrupt"

	"github.com/google/jsonschema-go/jsonschema"
)

type addTool struct{}

func (addTool) Name() string { return "add" }

func (addTool) Definition() nodes.ToolDefinition {
	return nodes.ToolDefinition{
		Type:        "function",
		Name:        "add",
		Description: "Add two numbers.",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"a": {Type: "number"},
				"b": {Type: "number"},
			},
			Required: []string{"a", "b"},
		},
	}
}

func (addTool) Execute(ctx context.Context, call nodes.ToolCall, events *[]graph.Event, emit graph.EmitFunc) (nodes.ToolResult, error) {
	var input struct {
		A float64 `json:"a"`
		B float64 `json:"b"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return nodes.ToolResult{}, err
	}

	review, err := interrupt.Interrupt(ctx, events, emit, interrupt.InterruptRequested{
		ID:    "add.review." + call.ID,
		Kind:  "tool.review",
		Value: call.Arguments,
	})
	if err != nil {
		return nodes.ToolResult{}, err
	}
	var decision struct {
		Approved bool `json:"approved"`
	}
	if err := json.Unmarshal(review.Value, &decision); err != nil {
		return nodes.ToolResult{}, err
	}
	if !decision.Approved {
		return nodes.ToolResult{}, errors.New("addition rejected")
	}

	return nodes.ToolResult{Content: []nodes.ContentPart{{
		Type: nodes.TextContentType,
		Text: strconv.FormatFloat(input.A+input.B, 'f', -1, 64),
	}}}, nil
}
