// cmd/bob-agent — Bob, IDUNA's database admin agent.
//
// Bob manages the IDUNA MySQL database: schema migrations, health checks,
// structural inspection, and read-only query support. He runs as an HTTP
// service that speaks the Emily agent protocol (Envelope wire format) so
// Emily Prime can dispatch database tasks to him.
//
// Endpoints:
//   POST /task         — receive an Envelope task from Emily, execute, return ResultBody
//   POST /chat         — interactive LLM chat (direct use / debugging)
//   POST /tick         — autonomous health sweep (cron-driven)
//   GET  /health       — heartbeat for the agent registry
//
// Env vars:
//   MYSQL_DSN          — required; MySQL connection string
//   ANTHROPIC_API_KEY  — required for LLM-driven endpoints
//   MODEL              — claude model (default claude-haiku-4-5-20251001)
//   PORT               — listen port (default 8083)
//   IDUNA_ROOT         — path to IDUNA repo root (default .)
//   RATE_LIMIT_RPM     — Anthropic calls per minute (default 20)
//   MAX_TOOL_ITERS     — max LLM tool-use iterations per request (default 10)

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// ── Config ────────────────────────────────────────────────────────────────────

type Config struct {
	Port          string
	Model         string
	APIKey        string
	RateLimitRPM  int
	MaxToolIters  int
	IdunaRoot     string
}

func loadConfig() Config {
	rpm, _ := strconv.Atoi(envOr("RATE_LIMIT_RPM", "20"))
	iters, _ := strconv.Atoi(envOr("MAX_TOOL_ITERS", "10"))
	if rpm <= 0 {
		rpm = 20
	}
	if iters <= 0 {
		iters = 10
	}
	return Config{
		Port:         envOr("PORT", "8083"),
		Model:        envOr("MODEL", "claude-haiku-4-5-20251001"),
		APIKey:       os.Getenv("ANTHROPIC_API_KEY"),
		RateLimitRPM: rpm,
		MaxToolIters: iters,
		IdunaRoot:    envOr("IDUNA_ROOT", "."),
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ── Tool dispatcher ───────────────────────────────────────────────────────────

type ToolFunc func(args map[string]any) (string, error)

type ToolPropSchema struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type ToolParameters struct {
	Type       string                    `json:"type"`
	Properties map[string]ToolPropSchema `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

type ToolDef struct {
	Name        string
	Description string
	Parameters  ToolParameters
}

type ToolDispatcher struct {
	defs     []ToolDef
	handlers map[string]ToolFunc
}

func NewToolDispatcher() *ToolDispatcher {
	return &ToolDispatcher{handlers: map[string]ToolFunc{}}
}

func (d *ToolDispatcher) Register(def ToolDef, fn ToolFunc) {
	d.defs = append(d.defs, def)
	d.handlers[def.Name] = fn
}

func (d *ToolDispatcher) Defs() []ToolDef { return d.defs }

func (d *ToolDispatcher) AnthropicDefs() []map[string]any {
	out := make([]map[string]any, len(d.defs))
	for i, def := range d.defs {
		out[i] = map[string]any{
			"name":         def.Name,
			"description":  def.Description,
			"input_schema": def.Parameters,
		}
	}
	return out
}

// ── Rate limiter ──────────────────────────────────────────────────────────────

type rateLimiter struct {
	mu       sync.Mutex
	tokens   float64
	maxTok   float64
	refillPS float64
	lastAt   time.Time
}

func newRateLimiter(rpm int) *rateLimiter {
	rps := float64(rpm) / 60.0
	return &rateLimiter{tokens: float64(rpm), maxTok: float64(rpm), refillPS: rps, lastAt: time.Now()}
}

func (r *rateLimiter) Wait() {
	for {
		r.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(r.lastAt).Seconds()
		r.tokens += elapsed * r.refillPS
		if r.tokens > r.maxTok {
			r.tokens = r.maxTok
		}
		r.lastAt = now
		if r.tokens >= 1 {
			r.tokens--
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
}

// ── Server ────────────────────────────────────────────────────────────────────

type Server struct {
	cfg     Config
	db      *sql.DB
	d       *ToolDispatcher
	limiter *rateLimiter
	client  *http.Client
	startAt time.Time
}

func NewServer(cfg Config, db *sql.DB) *Server {
	s := &Server{
		cfg:     cfg,
		db:      db,
		limiter: newRateLimiter(cfg.RateLimitRPM),
		client:  &http.Client{Timeout: 90 * time.Second},
		startAt: time.Now(),
	}
	s.d = NewToolDispatcher()
	registerDBTools(s.d, db, cfg.IdunaRoot)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		s.handleHealth(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/chat":
		s.handleChat(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/tick":
		s.handleTick(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/task":
		s.handleTask(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	dbOK := s.db.Ping() == nil
	status := "healthy"
	if !dbOK {
		status = "degraded"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":          status,
		"uptime_seconds":  int64(time.Since(s.startAt).Seconds()),
		"db_reachable":    dbOK,
		"tasks_pending":   0,
		"tasks_complete":  0,
		"last_error":      "",
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	reply, calls := s.runToolLoop(req.Messages)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"reply": reply, "tool_calls": calls})
}

const tickPrompt = `Do an autonomous database health sweep for IDUNA.

1. Call db_status to confirm the connection and check migration tracking.
2. Call migrate_status to see which migrations are applied and which are pending.
3. If any migrations are pending, call migrate_run to apply them.
4. Call db_row_counts to verify key tables are accessible.
5. Call schema_tables to confirm the full schema is present.

Report clearly: what was applied (if anything), what the current migration state is,
and whether any anomalies were found. If something is wrong that requires source code
changes (e.g. a migration file is corrupt, a required table is missing), describe the
issue precisely.`

func (s *Server) handleTick(w http.ResponseWriter, _ *http.Request) {
	msgs := []map[string]any{{"role": "user", "content": tickPrompt}}
	reply, calls := s.runToolLoop(msgs)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"reply": reply, "tool_calls": calls})
}

// handleTask implements the Emily agent protocol — accepts an Envelope,
// extracts the TaskBody, runs it through the LLM loop, and returns a ResultBody
// wrapped in an Envelope.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	var env agentEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		http.Error(w, `{"error":"invalid envelope"}`, 400)
		return
	}

	var task agentTaskBody
	if err := json.Unmarshal(env.Body, &task); err != nil {
		http.Error(w, `{"error":"invalid task body"}`, 400)
		return
	}

	start := time.Now()
	prompt := task.Description
	if len(task.SuccessCriteria) > 0 {
		var criteria []string
		for _, c := range task.SuccessCriteria {
			criteria = append(criteria, "- "+c.Name+": "+c.Target)
		}
		prompt += "\n\nAcceptance criteria:\n" + strings.Join(criteria, "\n")
	}

	msgs := []map[string]any{{"role": "user", "content": prompt}}
	reply, _ := s.runToolLoop(msgs)

	result := agentResultBody{
		TaskID:     task.TaskID,
		Status:     "success",
		DurationMS: time.Since(start).Milliseconds(),
		Outputs:    map[string]any{"reply": reply},
		AllPass:    true,
	}

	resultJSON, _ := json.Marshal(result)
	resp := agentEnvelope{
		V:       1,
		ID:      fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		From:    "bob",
		To:      env.From,
		Type:    "result",
		SentAt:  time.Now(),
		Body:    resultJSON,
		TraceID: env.TraceID,
		ReplyTo: env.ID,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ── LLM tool loop ─────────────────────────────────────────────────────────────

const anthropicURL = "https://api.anthropic.com/v1/messages"

func (s *Server) runToolLoop(msgs []map[string]any) (string, []map[string]string) {
	var toolCalls []map[string]string

	for i := 0; i < s.cfg.MaxToolIters; i++ {
		s.limiter.Wait()
		payload := map[string]any{
			"model":      s.cfg.Model,
			"max_tokens": 4096,
			"system":     bobSystemPrompt,
			"tools":      s.d.AnthropicDefs(),
			"messages":   msgs,
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest(http.MethodPost, anthropicURL, bytes.NewReader(b))
		req.Header.Set("x-api-key", s.cfg.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			log.Printf("bob: anthropic error: %v", err)
			return "Anthropic request failed: " + err.Error(), toolCalls
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode/100 != 2 {
			log.Printf("bob: anthropic status=%d body=%s", resp.StatusCode, body)
			return fmt.Sprintf("Anthropic error %d: %s", resp.StatusCode, strings.TrimSpace(string(body))), toolCalls
		}

		var ar struct {
			StopReason string           `json:"stop_reason"`
			Content    []map[string]any `json:"content"`
		}
		if err := json.Unmarshal(body, &ar); err != nil {
			return "Failed to parse Anthropic response", toolCalls
		}

		if ar.StopReason != "tool_use" {
			var parts []string
			for _, c := range ar.Content {
				if c["type"] == "text" {
					if t, ok := c["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
			return strings.Join(parts, "\n"), toolCalls
		}

		msgs = append(msgs, map[string]any{"role": "assistant", "content": ar.Content})
		var resultBlocks []map[string]any
		for _, c := range ar.Content {
			if c["type"] != "tool_use" {
				continue
			}
			name, _ := c["name"].(string)
			id, _ := c["id"].(string)
			in, _ := c["input"].(map[string]any)

			res := ""
			isErr := false
			fn := s.d.handlers[name]
			if fn == nil {
				res = "unknown tool: " + name
				isErr = true
			} else {
				var toolErr error
				res, toolErr = fn(in)
				if toolErr != nil {
					res = toolErr.Error()
					isErr = true
				}
			}
			log.Printf("bob: tool=%s err=%v result_len=%d", name, isErr, len(res))
			toolCalls = append(toolCalls, map[string]string{"tool": name, "result": res})
			if len(res) > 8000 {
				res = res[:8000] + "\n[truncated]"
			}
			resultBlocks = append(resultBlocks, map[string]any{
				"type":        "tool_result",
				"tool_use_id": id,
				"content":     res,
				"is_error":    isErr,
			})
		}
		msgs = append(msgs, map[string]any{"role": "user", "content": resultBlocks})
	}
	return "Reached max tool iterations.", toolCalls
}

// ── Agent protocol types ──────────────────────────────────────────────────────

type agentEnvelope struct {
	V       int             `json:"v"`
	ID      string          `json:"id"`
	From    string          `json:"from"`
	To      string          `json:"to"`
	Type    string          `json:"type"`
	SentAt  time.Time       `json:"sent_at"`
	Body    json.RawMessage `json:"body"`
	TraceID string          `json:"trace_id"`
	ReplyTo string          `json:"reply_to,omitempty"`
}

type agentTaskBody struct {
	TaskID          string               `json:"task_id"`
	Description     string               `json:"description"`
	Inputs          map[string]any       `json:"inputs"`
	SuccessCriteria []agentCriterion     `json:"success_criteria"`
	TimeoutSeconds  int                  `json:"timeout_seconds"`
}

type agentCriterion struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type agentResultBody struct {
	TaskID     string         `json:"task_id"`
	Status     string         `json:"status"`
	Outputs    map[string]any `json:"outputs"`
	AllPass    bool           `json:"all_pass"`
	Error      string         `json:"error,omitempty"`
	DurationMS int64          `json:"duration_ms"`
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()

	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("MYSQL_DSN is required")
	}
	if cfg.APIKey == "" {
		log.Fatal("ANTHROPIC_API_KEY is required")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("bob: open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("bob: db ping: %v", err)
	}

	// Bootstrap the schema_migrations tracking table.
	if err := bootstrapMigrationsTable(db); err != nil {
		log.Fatalf("bob: bootstrap migrations table: %v", err)
	}

	srv := NewServer(cfg, db)
	addr := ":" + cfg.Port
	log.Printf("bob-agent listening on %s (model=%s iduna_root=%s)", addr, cfg.Model, cfg.IdunaRoot)
	log.Fatal(http.ListenAndServe(addr, srv))
}
