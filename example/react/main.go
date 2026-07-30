package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/yclw/gag/adk/react"
	"github.com/yclw/gag/graph"
	"github.com/yclw/gag/nodes"
	"github.com/yclw/gag/nodes/interrupt"
	"github.com/yclw/gag/nodes/models/openai"

	_ "modernc.org/sqlite"
)

type server struct {
	db    *sql.DB
	agent *graph.Graph
	mu    sync.Mutex
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

	agent, err := react.New(model, addTool{})
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

	s := &server{db: db, agent: agent}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /run", s.run)
	mux.HandleFunc("POST /chat", s.chat)
	mux.HandleFunc("POST /review", s.review)

	log.Printf("listening on %s", ":8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func (s *server) run(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events, err := s.load(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.stream(w, r, events)
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || strings.TrimSpace(in.Message) == "" {
		http.Error(w, "invalid message", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	events, err := s.load(r.Context())
	if err == nil {
		var value []byte
		value, err = json.Marshal(nodes.UserMessageInput{Content: []nodes.ContentPart{{
			Type: nodes.TextContentType, Text: in.Message,
		}}})
		if err == nil {
			err = interrupt.ResumeInterrupt(r.Context(), &events, interrupt.InterruptResumed{
				RequestID: "react.user",
				Value:     value,
			})
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.stream(w, r, events)
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

	s.mu.Lock()
	defer s.mu.Unlock()

	events, err := s.load(r.Context())
	if err == nil {
		var value []byte
		value, err = json.Marshal(struct {
			Approved bool `json:"approved"`
		}{in.Approved})
		if err == nil {
			err = interrupt.ResumeInterrupt(r.Context(), &events, interrupt.InterruptResumed{
				RequestID: in.RequestID,
				Value:     value,
			})
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.stream(w, r, events)
}

func (s *server) stream(w http.ResponseWriter, r *http.Request, events []graph.Event) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	err := s.agent.Run(r.Context(), &events, func(_ context.Context, event graph.Event) error {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err == nil {
		err = s.save(r.Context(), events)
	}
	if err != nil {
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
		flusher.Flush()
	}
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
