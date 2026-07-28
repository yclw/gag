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

func NewEvent(typ string, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("event %q: encode payload: %w", typ, err)
	}
	return Event{Type: typ, Data: data}, nil
}

type EmitFunc func(ctx context.Context, event Event) error

type RunAction uint8

const (
	RunContinue RunAction = iota
	RunSuspend
	RunPause
	RunReset
)

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
