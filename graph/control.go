package graph

import (
	"errors"
	"fmt"
)

type RunAction uint8

const (
	RunContinue RunAction = iota
	RunSuspend
	RunPause
	RunReset
)

type ControlSignal struct {
	Action RunAction
	Cause  error
}

func (s *ControlSignal) Error() string {
	if s == nil {
		return "graph: control signal"
	}
	if s.Cause == nil {
		return fmt.Sprintf("graph: control signal: action=%d", s.Action)
	}
	return fmt.Sprintf("graph: control signal: action=%d: %v", s.Action, s.Cause)
}

func (s *ControlSignal) Unwrap() error {
	if s == nil {
		return nil
	}
	return s.Cause
}

func Suspend(cause error) error {
	return &ControlSignal{
		Action: RunSuspend,
		Cause:  cause,
	}
}

func Pause(cause error) error {
	return &ControlSignal{
		Action: RunPause,
		Cause:  cause,
	}
}

func Reset(cause error) error {
	return &ControlSignal{
		Action: RunReset,
		Cause:  cause,
	}
}

func ControlAction(err error) (RunAction, bool) {
	var signal *ControlSignal
	if !errors.As(err, &signal) || signal == nil {
		return RunContinue, false
	}
	return signal.Action, true
}
