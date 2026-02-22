package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

type EventType uint8

const (
	EventUnknown EventType = iota
	EventXIngest
	EventEmilyHold
	EventEmilySuggestion
)

type Event struct {
	ID        uint64    `json:"id"`
	Timestamp int64     `json:"ts"`
	Type      EventType `json:"type"`
	Key       string    `json:"key"`
}

type EventDTO struct {
	ID        uint64 `json:"id"`
	Timestamp int64  `json:"ts"`
	Type      string `json:"type"`
	Key       string `json:"key"`
	Summary   string `json:"summary"`
}

type EventStore struct {
	path string
}

func OpenEventStore(path string) (*EventStore, error) {
	return &EventStore{path: path}, nil
}

func (s *EventStore) Replay(fn func(Event) error) error {
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if err := fn(event); err != nil {
			return err
		}
	}

	return scanner.Err()
}

func eventTypeName(eventType EventType) string {
	switch eventType {
	case EventXIngest:
		return "X ingest"
	case EventEmilyHold:
		return "Emily hold"
	case EventEmilySuggestion:
		return "Emily suggestion"
	default:
		return "System"
	}
}

func summarize(event Event) string {
	switch event.Type {
	case EventXIngest:
		return "Incoming X event"
	case EventEmilyHold:
		return "Emily: holding for later"
	case EventEmilySuggestion:
		return "Emily: draft available"
	default:
		return "System event"
	}
}

func collectEvents(store *EventStore, since uint64) []EventDTO {
	var out []EventDTO

	_ = store.Replay(func(event Event) error {
		if event.ID <= since {
			return nil
		}

		out = append(out, EventDTO{
			ID:        event.ID,
			Timestamp: event.Timestamp,
			Type:      eventTypeName(event.Type),
			Key:       event.Key,
			Summary:   summarize(event),
		})
		return nil
	})

	return out
}

func serveUI(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(".", "index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, path)
}

func main() {
	store, err := OpenEventStore("events.log")
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/", serveUI)
	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		sinceStr := r.URL.Query().Get("since")
		var since uint64
		if sinceStr != "" {
			parsed, err := strconv.ParseUint(sinceStr, 10, 64)
			if err == nil {
				since = parsed
			}
		}

		events := collectEvents(store, since)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
			return
		}
	})

	log.Println("unagent viewer listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
