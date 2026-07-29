package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func NewEvent[T any](typ string, payload T) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("event %q: encode payload: %w", typ, err)
	}
	return Event{Type: typ, Data: data}, nil
}

func DecodeEvent[T any](event Event, expected string) (T, error) {
	var payload T
	if event.Type != expected {
		return payload, fmt.Errorf("event: type is %q, want %q", event.Type, expected)
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return payload, fmt.Errorf("event %q: decode payload: %w", event.Type, err)
	}
	return payload, nil
}

type EmitFunc func(ctx context.Context, event Event) error

type NodeResult struct {
	Action RunAction
}

type Node interface {
	ID() string
	Run(ctx context.Context, events *[]Event, emit EmitFunc) (NodeResult, error)
}

type NextFunc func(ctx context.Context, events []Event) (string, error)

type Graph struct {
	nodes map[string]Node
	next  NextFunc
}

func (g *Graph) Run(ctx context.Context, events *[]Event, emit EmitFunc) error {
	for {
		id, err := g.next(ctx, *events)
		if err != nil {
			return err
		}
		node := g.nodes[id]
		if node == nil {
			return errors.New("unknown next node")
		}
		worker := append([]Event(nil), (*events)...)
		result, err := node.Run(ctx, &worker, emit)
		if err != nil {
			return err
		}
		*events = worker
		if result.Action != RunContinue {
			return nil
		}
	}
}
