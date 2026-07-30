package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yclw/gag/adk/react"
	"github.com/yclw/gag/graph"
	"github.com/yclw/gag/nodes"
	"github.com/yclw/gag/nodes/interrupt"
	"github.com/yclw/gag/nodes/models/openai"

	_ "modernc.org/sqlite"
)

//go:embed index.html
var indexHTML []byte

var errUserCanceled = errors.New("cancelled by user")

type server struct {
	ctx     context.Context
	db      *sql.DB
	agent   *graph.Graph
	mu      sync.Mutex
	current *execution

	subscribers map[*subscriber]struct{}
}

type execution struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	output []graph.Event
}

type subscriber struct {
	events chan graph.Event
}

func main() {
	model, err := openai.New(openai.Config{
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		Model:   os.Getenv("OPENAI_MODEL"),
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
	})
	if err != nil {
		log.Fatal(err)
	}

	const systemPrompt = `You are a math assistant.`
	agent, err := react.New(model, systemPrompt, addTool{})
	if err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("sqlite", "react.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		events BLOB NOT NULL
	)`); err != nil {
		log.Fatal(err)
	}

	s := &server{
		ctx:         context.Background(),
		db:          db,
		agent:       agent,
		subscribers: make(map[*subscriber]struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /run", s.run)
	mux.HandleFunc("POST /chat", s.chat)
	mux.HandleFunc("POST /review", s.review)
	mux.HandleFunc("POST /cancel", s.cancel)
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	log.Printf("listening on %s", ":8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func (s *server) run(w http.ResponseWriter, r *http.Request) {
	s.control(w, nil)
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || strings.TrimSpace(in.Message) == "" {
		http.Error(w, "invalid message", http.StatusBadRequest)
		return
	}

	value, err := json.Marshal(nodes.UserMessageInput{Content: []nodes.ContentPart{{
		Type: nodes.TextContentType, Text: in.Message,
	}}})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.control(w, func(ctx context.Context, events *[]graph.Event) error {
		return interrupt.ResumeInterrupt(ctx, events, interrupt.InterruptResumed{
			RequestID: "react.user",
			Value:     value,
		})
	})
}

func (s *server) review(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID string `json:"request_id"`
		Approved  bool   `json:"approved"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || strings.TrimSpace(in.RequestID) == "" {
		http.Error(w, "invalid review", http.StatusBadRequest)
		return
	}

	value, err := json.Marshal(struct {
		Approved bool `json:"approved"`
	}{in.Approved})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.control(w, func(ctx context.Context, events *[]graph.Event) error {
		return interrupt.ResumeInterrupt(ctx, events, interrupt.InterruptResumed{
			RequestID: in.RequestID,
			Value:     value,
		})
	})
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	exec, started := s.start(errUserCanceled)
	if !started {
		exec.cancel(errUserCanceled)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	go s.execute(exec, nil)
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) control(w http.ResponseWriter, apply func(context.Context, *[]graph.Event) error) {
	exec, ok := s.start(nil)
	if !ok {
		http.Error(w, "busy", http.StatusConflict)
		return
	}
	go s.execute(exec, apply)
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) start(cause error) (*execution, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current != nil {
		return s.current, false
	}

	ctx, cancel := context.WithCancelCause(s.ctx)
	if cause != nil {
		cancel(cause)
	}
	exec := &execution{
		ctx:    ctx,
		cancel: cancel,
	}
	s.current = exec
	return exec, true
}

func (s *server) execute(exec *execution, control func(context.Context, *[]graph.Event) error) {
	defer exec.cancel(nil)

	events, err := s.load(s.ctx)
	if err == nil && control != nil {
		err = control(exec.ctx, &events)
		if cause := context.Cause(exec.ctx); cause != nil && errors.Is(err, cause) {
			err = nil
		}
	}
	if err == nil {
		err = s.agent.Run(exec.ctx, &events, func(_ context.Context, event graph.Event) error {
			return s.emit(exec, event)
		})
	}

	s.mu.Lock()
	if err == nil {
		err = s.save(s.ctx, events)
	}
	if err != nil {
		event, encodeErr := graph.NewEvent("execution.error", map[string]string{"error": err.Error()})
		if encodeErr == nil {
			exec.output = append(exec.output, event)
			s.publishLocked(event)
		}
	}
	if s.current == exec {
		s.current = nil
	}
	s.mu.Unlock()
}

func (s *server) emit(exec *execution, event graph.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current != exec {
		return nil
	}
	exec.output = append(exec.output, event)
	s.publishLocked(event)
	return nil
}

func (s *server) publishLocked(event graph.Event) {
	for sub := range s.subscribers {
		select {
		case sub.events <- event:
		default:
			delete(s.subscribers, sub)
			close(sub.events)
		}
	}
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	history, output, sub, err := s.subscribe(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer s.unsubscribe(sub)

	snapshot, err := graph.NewEvent("session.snapshot", history)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	if writeEvent(w, snapshot) != nil {
		return
	}
	for _, event := range output {
		if writeEvent(w, event) != nil {
			return
		}
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, ok := <-sub.events:
			if !ok || writeEvent(w, event) != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) subscribe(ctx context.Context) ([]graph.Event, []graph.Event, *subscriber, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history, err := s.load(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if history == nil {
		history = []graph.Event{}
	}
	var output []graph.Event
	if s.current != nil {
		output = append(output, s.current.output...)
	}
	sub := &subscriber{events: make(chan graph.Event, 256)}
	if s.subscribers == nil {
		s.subscribers = make(map[*subscriber]struct{})
	}
	s.subscribers[sub] = struct{}{}
	return history, output, sub, nil
}

func (s *server) unsubscribe(sub *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subscribers[sub]; ok {
		delete(s.subscribers, sub)
		close(sub.events)
	}
}

func writeEvent(w io.Writer, event graph.Event) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
	return err
}

func (s *server) load(ctx context.Context) ([]graph.Event, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT events FROM session WHERE id = 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	var events []graph.Event
	if err == nil {
		err = json.Unmarshal(data, &events)
	}
	return events, err
}

func (s *server) save(ctx context.Context, events []graph.Event) error {
	data, err := json.Marshal(events)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO session(id, events) VALUES(1, ?)
		ON CONFLICT(id) DO UPDATE SET events = excluded.events`, data)
	return err
}
