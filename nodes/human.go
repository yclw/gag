package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"gag/graph"
	"gag/nodes/interrupt"
)

const UserMessageInterruptKind string = "user.message"

type UserMessageNode struct {
	NodeID string
}

type UserMessageInput struct {
	Content []ContentPart `json:"content,omitempty"`
}

var _ graph.Node = (*UserMessageNode)(nil)

func NewUserMessageNode(id string) (*UserMessageNode, error) {
	return &UserMessageNode{NodeID: id}, nil
}

func (n *UserMessageNode) ID() string {
	return n.NodeID
}

func (n *UserMessageNode) Run(ctx context.Context, events *[]graph.Event, emit graph.EmitFunc) (graph.NodeResult, error) {
	response, err := interrupt.Interrupt(ctx, events, emit, interrupt.InterruptRequested{
		ID:   n.NodeID,
		Kind: UserMessageInterruptKind,
	})
	if action, ok := graph.ControlAction(err); ok {
		return graph.NodeResult{Action: action}, nil
	}
	if err != nil {
		return graph.NodeResult{}, err
	}

	var input UserMessageInput
	if err := json.Unmarshal(response.Value, &input); err != nil {
		return graph.NodeResult{}, fmt.Errorf("user message: decode input: %w", err)
	}

	event, err := graph.NewEvent(ModelMessageEventType, ModelMessage{
		Role:     UserRole,
		Content:  input.Content,
		Metadata: response.Metadata,
	})
	if err != nil {
		return graph.NodeResult{}, err
	}
	*events = append(*events, event)
	if err := emit(ctx, event); err != nil {
		return graph.NodeResult{}, err
	}

	return graph.NodeResult{Action: graph.RunContinue}, nil
}
