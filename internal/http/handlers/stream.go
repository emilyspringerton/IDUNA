package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"iduna/internal/userlog"
)

// UserEventStreamHandler serves GET /api/v1/stream/user-events as a
// Server-Sent Events (SSE) stream of user-event log records. Colab notebooks
// can subscribe and react to local_user.* events in real time.
//
// Query params:
//
//	from_seq — start sequence (default: 1, i.e. full replay from beginning)
//	timeout  — max seconds to hold the connection open (default 300, max 3600)
type UserEventStreamHandler struct {
	Log userlog.EventLog
}

func (h *UserEventStreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse from_seq query param.
	fromSeq := uint64(1)
	if s := r.URL.Query().Get("from_seq"); s != "" {
		if n, err := strconv.ParseUint(s, 10, 64); err == nil && n > 0 {
			fromSeq = n
		}
	}

	// Parse timeout (seconds); cap at 3600.
	timeout := 300 * time.Second
	if s := r.URL.Query().Get("timeout"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > 3600 {
				n = 3600
			}
			timeout = time.Duration(n) * time.Second
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	// Initial comment to establish the connection.
	fmt.Fprintf(w, ": IDUNA user-event stream — connected from_seq=%d\n\n", fromSeq)
	flusher.Flush()

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	seq := fromSeq
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(w, "event: eof\ndata: {\"reason\":\"timeout\"}\n\n")
			flusher.Flush()
			return
		case <-poll.C:
			recs, err := h.Log.ReadFrom(ctx, seq, 64)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":%q}\n\n", err.Error())
				flusher.Flush()
				continue
			}
			for _, rec := range recs {
				payload, _ := json.Marshal(rec)
				fmt.Fprintf(w, "id: %d\nevent: user_event\ndata: %s\n\n", rec.Sequence, payload)
				seq = rec.Sequence + 1
			}
			if len(recs) > 0 {
				flusher.Flush()
			}
		}
	}
}
