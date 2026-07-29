package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
)

const cursorEventType = "graph.compose.cursor"

type CursorEvent struct {
	GraphID   string `json:"graph_id"`
	NodeID    string `json:"node_id"`
	Completed bool   `json:"completed"`
}

type Router func(context.Context, []Event) (string, error)

type Builder struct {
	id     string
	nodes  map[string]Node
	routes map[string]Router
	start  Router
	err    error
}

func NewBuilder(id string, nodes ...Node) *Builder {
	b := &Builder{
		id:     id,
		nodes:  make(map[string]Node),
		routes: make(map[string]Router),
	}
	return b.Add(nodes...)
}

func (b *Builder) Add(nodes ...Node) *Builder {
	if b.err != nil {
		return b
	}
	for _, node := range nodes {
		b.nodes[node.ID()] = node
	}
	return b
}

func (b *Builder) Route(from string, router Router) *Builder {
	if b.err != nil {
		return b
	}
	b.routes[from] = router
	return b
}

func (b *Builder) Link(from, to string) *Builder {
	return b.Route(from, func(context.Context, []Event) (string, error) {
		return to, nil
	})
}

func (b *Builder) StartRouter(router Router) *Builder {
	if b.err != nil {
		return b
	}
	b.start = router
	return b
}

func (b *Builder) StartNode(id string) *Builder {
	return b.StartRouter(func(context.Context, []Event) (string, error) {
		return id, nil
	})
}

func (b *Builder) Build() (*Graph, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.start == nil {
		return nil, errors.New("compose: start router is not configured")
	}
	if len(b.nodes) == 0 {
		return nil, errors.New("compose: no nodes")
	}

	nodes := make(map[string]Node, len(b.nodes))
	for id, node := range b.nodes {
		nodes[id] = wrappedNode{
			Node:    node,
			graphID: b.id,
		}
	}

	routes := make(map[string]Router, len(b.routes))
	maps.Copy(routes, b.routes)

	graphID := b.id
	start := b.start

	next := func(ctx context.Context, events []Event) (string, error) {
		cursor, worker, found, err := splitCursor(events, graphID)
		if err != nil {
			return "", err
		}

		if !found {
			return start(ctx, worker)
		}

		if !cursor.Completed {
			return cursor.NodeID, nil
		}

		router := routes[cursor.NodeID]
		if router == nil {
			return "", fmt.Errorf("compose: no route after node %q", cursor.NodeID)
		}

		return router(ctx, worker)
	}

	return &Graph{
		nodes: nodes,
		next:  next,
	}, nil
}

func splitCursor(events []Event, graphID string) (CursorEvent, []Event, bool, error) {
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]

		if event.Type != cursorEventType {
			continue
		}
		var candidate CursorEvent
		if err := json.Unmarshal(event.Data, &candidate); err != nil {
			return CursorEvent{}, nil, false, fmt.Errorf("compose: decode cursor event at index %d: %w", i, err)
		}
		if candidate.GraphID != graphID {
			continue
		}
		worker := make([]Event, 0, len(events)-1)
		worker = append(worker, events[:i]...)
		worker = append(worker, events[i+1:]...)
		return candidate, worker, true, nil
	}
	return CursorEvent{}, events, false, nil
}

func removeCursor(events []Event, graphID string) ([]Event, error) {
	_, worker, ok, err := splitCursor(events, graphID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return events, nil
	}
	return worker, nil
}

type wrappedNode struct {
	Node
	graphID string
}

func (n wrappedNode) Run(ctx context.Context, events *[]Event, emit EmitFunc) (NodeResult, error) {
	worker, err := removeCursor(*events, n.graphID)
	if err != nil {
		return NodeResult{}, err
	}
	*events = worker
	result, err := n.Node.Run(ctx, events, emit)
	if err != nil {
		return result, err
	}
	if result.Action == RunReset {
		return result, nil
	}
	cursor, err := NewEvent(cursorEventType, CursorEvent{
		GraphID:   n.graphID,
		NodeID:    n.Node.ID(),
		Completed: result.Action != RunSuspend,
	})
	if err != nil {
		return result, err
	}
	*events = append(*events, cursor)
	return result, nil
}
