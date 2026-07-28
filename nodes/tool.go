package nodes

import (
	"context"
	"encoding/json"
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

var _ graph.Node = (*ToolNode)(nil)

func (n *ToolNode) ID() string {
	return n.NodeID
}

func (n *ToolNode) Run(ctx context.Context, events *[]graph.Event, emit graph.EmitFunc) (graph.NodeResult, error) {
	// TODO
	panic("")
}
