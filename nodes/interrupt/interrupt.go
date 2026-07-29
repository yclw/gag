package interrupt

import (
	"context"
	"encoding/json"
	"fmt"

	"gag/graph"
)

const (
	InterruptRequestedEventType string = "interrupt.requested"
	InterruptResumedEventType   string = "interrupt.resumed"
)

type InterruptRequested struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type InterruptResumed struct {
	RequestID string          `json:"request_id"`
	Value     json.RawMessage `json:"value"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

func Interrupt(ctx context.Context, events *[]graph.Event, emit graph.EmitFunc, request InterruptRequested) (InterruptResumed, error) {
	requestIndexes, responseIndexes, response, err := findInterruptEvents(*events, request.ID)
	if err != nil {
		return InterruptResumed{}, err
	}
	if cause := context.Cause(ctx); cause != nil {
		if len(requestIndexes) > 0 || len(responseIndexes) > 0 {
			*events = removeEvents(*events, requestIndexes, responseIndexes)
		}
		return InterruptResumed{}, cause
	}
	if len(responseIndexes) > 0 {
		*events = removeEvents(*events, requestIndexes, responseIndexes)
		return response, nil
	}

	if len(requestIndexes) == 0 {
		event, err := graph.NewEvent(InterruptRequestedEventType, request)
		if err != nil {
			return InterruptResumed{}, err
		}
		*events = append(*events, event)
		if err := emit(ctx, event); err != nil {
			return InterruptResumed{}, err
		}
	}

	return InterruptResumed{}, graph.Suspend(nil)
}

func ResumeInterrupt(ctx context.Context, events *[]graph.Event, response InterruptResumed) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	requestIndexes, responseIndexes, _, err := findInterruptEvents(*events, response.RequestID)
	if err != nil {
		return err
	}
	if len(requestIndexes) == 0 {
		return fmt.Errorf("interrupt: request %q not found", response.RequestID)
	}
	if len(responseIndexes) > 0 {
		return fmt.Errorf("interrupt: request %q is already resumed", response.RequestID)
	}

	event, err := graph.NewEvent(InterruptResumedEventType, response)
	if err != nil {
		return err
	}
	*events = append(*events, event)
	return nil
}

func findInterruptEvents(events []graph.Event, requestID string) ([]int, []int, InterruptResumed, error) {
	var requestIndexes []int
	var responseIndexes []int
	var response InterruptResumed

	for i, event := range events {
		switch event.Type {
		case InterruptRequestedEventType:
			request, decodeErr := graph.DecodeEvent[InterruptRequested](
				event,
				InterruptRequestedEventType,
			)
			if decodeErr != nil {
				return nil, nil, InterruptResumed{}, decodeErr
			}
			if request.ID == requestID {
				requestIndexes = append(requestIndexes, i)
			}

		case InterruptResumedEventType:
			candidate, decodeErr := graph.DecodeEvent[InterruptResumed](
				event,
				InterruptResumedEventType,
			)
			if decodeErr != nil {
				return nil, nil, InterruptResumed{}, decodeErr
			}
			if candidate.RequestID == requestID {
				responseIndexes = append(responseIndexes, i)
				if len(responseIndexes) == 1 {
					response = candidate
				}
			}
		}
	}
	return requestIndexes, responseIndexes, response, nil
}

func removeEvents(events []graph.Event, indexes ...[]int) []graph.Event {
	remove := make(map[int]struct{})
	for _, group := range indexes {
		for _, index := range group {
			remove[index] = struct{}{}
		}
	}

	worker := make([]graph.Event, 0, len(events)-len(remove))
	for i, event := range events {
		if _, ok := remove[i]; ok {
			continue
		}
		worker = append(worker, event)
	}
	return worker
}
