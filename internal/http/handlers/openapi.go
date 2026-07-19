package handlers

import (
	"encoding/json"
	"net/http"
)

// OpenAPIHandler serves the OpenAPI 3.1 spec at GET /api/v1/openapi.json.
// This is the machine-readable contract for the einhorn_sdk auto-generator.
type OpenAPIHandler struct{}

func (h *OpenAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(idunaOpenAPISpec)
}

// idunaOpenAPISpec is the OpenAPI 3.1 description of the IDUNA IAM API.
// Keep in sync with actual routes in main.go.
var idunaOpenAPISpec = map[string]any{
	"openapi": "3.1.0",
	"info": map[string]any{
		"title":       "IDUNA IAM API",
		"description": "Central trust authority for EINHORN_INDUSTRIAL. Issues ES256 JWTs for humans and M2M agents. Manages users, agents, Apples, HEIMDAL sprints, push tokens, and Google Drive.",
		"version":     "1.0.0",
		"contact": map[string]any{
			"name":  "Emily Prime",
			"email": "emilyspringerton@gmail.com",
		},
	},
	"servers": []map[string]any{
		{"url": "https://okemily.com", "description": "Public (via okemily.com's nginx proxy)"},
		{"url": "http://localhost:8080", "description": "Local development"},
	},
	"components": map[string]any{
		"securitySchemes": map[string]any{
			"bearerAuth": map[string]any{
				"type":         "http",
				"scheme":       "bearer",
				"bearerFormat": "JWT",
				"description":  "ES256 JWT issued by IDUNA /api/v1/auth/local or /api/v1/auth/agent",
			},
		},
		"schemas": map[string]any{
			"LocalUser": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"local_uid":    map[string]any{"type": "integer", "description": "Numeric user ID. 0 = webmaster (root)."},
					"email":        map[string]any{"type": "string", "format": "email"},
					"display_name": map[string]any{"type": "string"},
					"status":       map[string]any{"type": "string", "enum": []string{"active", "suspended", "deleted"}},
					"created_at":   map[string]any{"type": "string", "format": "date-time"},
					"updated_at":   map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"Apple": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          map[string]any{"type": "integer"},
					"type":        map[string]any{"type": "string", "enum": []string{"improvement", "observation", "audit", "escalation", "completion", "backlog_completion"}},
					"title":       map[string]any{"type": "string"},
					"body":        map[string]any{"type": "string"},
					"source_repo": map[string]any{"type": "string"},
					"agent_id":    map[string]any{"type": "string"},
					"created_at":  map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"Agent": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          map[string]any{"type": "string", "format": "uuid"},
					"name":        map[string]any{"type": "string"},
					"type":        map[string]any{"type": "string"},
					"status":      map[string]any{"type": "string"},
					"permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"created_at":  map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"ShankpitQueueStatus": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"state":          map[string]any{"type": "string", "enum": []string{"not_queued", "queuing", "matched"}},
					"queue_position": map[string]any{"type": "integer"},
					"queue_size":     map[string]any{"type": "integer"},
					"server_addr":    map[string]any{"type": "string", "description": "present only when state=matched"},
				},
			},
			"DriveFile": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":           map[string]any{"type": "string"},
					"name":         map[string]any{"type": "string"},
					"mime_type":    map[string]any{"type": "string"},
					"size":         map[string]any{"type": "string"},
					"web_view_link": map[string]any{"type": "string"},
					"created_time": map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"SprintItem": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":           map[string]any{"type": "integer"},
					"agent_name":   map[string]any{"type": "string"},
					"requirement":  map[string]any{"type": "string"},
					"status":       map[string]any{"type": "string", "enum": []string{"pending", "queued", "in_progress", "complete", "blocked"}},
					"roadmap_id":   map[string]any{"type": "string"},
					"apple_id":     map[string]any{"type": "integer"},
					"created_at":   map[string]any{"type": "string", "format": "date-time"},
					"updated_at":   map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"ErrorResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"error": map[string]any{"type": "string"},
				},
			},
			"TokenResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token":      map[string]any{"type": "string", "description": "ES256 JWT"},
					"expires_at": map[string]any{"type": "integer", "description": "Unix timestamp"},
					"sub":        map[string]any{"type": "string"},
				},
			},
		},
	},
	"security": []map[string]any{
		{"bearerAuth": []string{}},
	},
	"paths": map[string]any{
		// ── Auth ─────────────────────────────────────────────────────────────
		"/api/v1/auth/local": map[string]any{
			"post": map[string]any{
				"summary":  "Local password authentication",
				"tags":     []string{"auth"},
				"security": []map[string]any{},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"required": []string{"email", "password"},
								"properties": map[string]any{
									"email":    map[string]any{"type": "string", "format": "email"},
									"password": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "JWT issued",
						"content":     jsonSchema("TokenResponse"),
					},
					"401": errorResponse("Invalid credentials"),
				},
			},
		},
		"/api/v1/auth/agent": map[string]any{
			"post": map[string]any{
				"summary":  "M2M agent authentication",
				"tags":     []string{"auth"},
				"security": []map[string]any{},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"required": []string{"agent_name", "agent_secret"},
								"properties": map[string]any{
									"agent_name":   map[string]any{"type": "string"},
									"agent_secret": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "JWT issued",
						"content":     jsonSchema("TokenResponse"),
					},
					"401": errorResponse("Invalid agent credentials"),
				},
			},
		},
		// ── Users ─────────────────────────────────────────────────────────────
		"/api/v1/users": map[string]any{
			"get": map[string]any{
				"summary":  "List local users",
				"tags":     []string{"users"},
				"parameters": []map[string]any{
					{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 100}},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "User list", "content": jsonSchemaArray("LocalUser")},
					"403": errorResponse("Forbidden"),
				},
			},
			"post": map[string]any{
				"summary": "Create local user",
				"tags":    []string{"users"},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"required": []string{"email", "password"},
								"properties": map[string]any{
									"email":        map[string]any{"type": "string", "format": "email"},
									"password":     map[string]any{"type": "string"},
									"display_name": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"201": map[string]any{"description": "User created", "content": jsonSchema("LocalUser")},
					"409": errorResponse("Email already exists"),
				},
			},
		},
		"/api/v1/users/{uid}": map[string]any{
			"parameters": []map[string]any{
				{"name": "uid", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
			},
			"get": map[string]any{
				"summary": "Get local user by uid",
				"tags":    []string{"users"},
				"responses": map[string]any{
					"200": map[string]any{"description": "User", "content": jsonSchema("LocalUser")},
					"404": errorResponse("Not found"),
				},
			},
			"patch": map[string]any{
				"summary": "Update local user",
				"tags":    []string{"users"},
				"requestBody": map[string]any{
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"email":        map[string]any{"type": "string", "format": "email"},
									"display_name": map[string]any{"type": "string"},
									"password":     map[string]any{"type": "string"},
									"status":       map[string]any{"type": "string", "enum": []string{"active", "suspended"}},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Updated user", "content": jsonSchema("LocalUser")},
					"404": errorResponse("Not found"),
				},
			},
			"delete": map[string]any{
				"summary": "Soft-delete local user (sets status=deleted)",
				"tags":    []string{"users"},
				"responses": map[string]any{
					"204": map[string]any{"description": "Deleted"},
					"403": errorResponse("Cannot delete webmaster"),
					"404": errorResponse("Not found"),
				},
			},
		},
		// ── Apples ────────────────────────────────────────────────────────────
		"/api/v1/apples": map[string]any{
			"post": map[string]any{
				"summary": "File an Apple",
				"tags":    []string{"apples"},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"required": []string{"type", "title"},
								"properties": map[string]any{
									"type":        map[string]any{"type": "string"},
									"title":       map[string]any{"type": "string"},
									"body":        map[string]any{"type": "string"},
									"source_repo": map[string]any{"type": "string"},
									"metadata":    map[string]any{"type": "object"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"201": map[string]any{"description": "Apple filed", "content": jsonSchema("Apple")},
				},
			},
			"get": map[string]any{
				"summary": "List Apples",
				"tags":    []string{"apples"},
				"parameters": []map[string]any{
					{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 50}},
					{"name": "apple_type", "in": "query", "schema": map[string]any{"type": "string"}},
					{"name": "source_repo", "in": "query", "schema": map[string]any{"type": "string"}},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Apple list", "content": jsonSchemaArray("Apple")},
				},
			},
		},
		"/api/v1/apples/{id}": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
			},
			"get": map[string]any{
				"summary": "Get single Apple",
				"tags":    []string{"apples"},
				"responses": map[string]any{
					"200": map[string]any{"description": "Apple", "content": jsonSchema("Apple")},
					"404": errorResponse("Not found"),
				},
			},
		},
		// ── Agents ────────────────────────────────────────────────────────────
		"/api/v1/agents": map[string]any{
			"get": map[string]any{
				"summary":     "List registered agents",
				"tags":        []string{"agents"},
				"description": "Returns all agents. Filter by ?type=emily_cluster to enumerate distributed Emily processes.",
				"parameters": []map[string]any{
					{"name": "type", "in": "query", "required": false, "schema": map[string]any{"type": "string"}, "description": "Filter by agent type (e.g. emily_cluster)"},
				},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Agent list", "content": jsonSchemaArray("Agent")},
				},
			},
		},
		"/api/v1/agents/{id}": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
			},
			"get": map[string]any{
				"summary":  "Get single agent by ID or name",
				"tags":     []string{"agents"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Agent", "content": jsonSchema("Agent")},
					"404": errorResponse("Not found"),
				},
			},
		},
		// ── SHANKPIT players ──────────────────────────────────────────────────
		"/api/v1/players/register": map[string]any{
			"post": map[string]any{
				"summary":  "Register or update a SHANKPIT player identity",
				"tags":     []string{"players"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"required": []string{"provider", "provider_sub"},
								"properties": map[string]any{
									"provider":     map[string]any{"type": "string", "example": "google"},
									"provider_sub": map[string]any{"type": "string"},
									"display_name": map[string]any{"type": "string"},
									"email":        map[string]any{"type": "string", "format": "email"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Player registered/updated", "content": map[string]any{
						"application/json": map[string]any{"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"player_id":    map[string]any{"type": "string"},
								"display_name": map[string]any{"type": "string"},
							},
						}},
					}},
					"400": errorResponse("Missing provider or provider_sub"),
				},
			},
		},
		"/api/v1/players/{id}": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "player_id UUID"},
			},
			"get": map[string]any{
				"summary":  "Get SHANKPIT player profile",
				"tags":     []string{"players"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Player profile with kills, deaths, kd_ratio, sessions"},
					"404": errorResponse("Not found"),
				},
			},
		},
		"/api/v1/players/{id}/session": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "player_id UUID"},
			},
			"post": map[string]any{
				"summary":     "Increment a player's session stats (kills/deaths)",
				"tags":        []string{"players"},
				"description": "Requires the shankpit.match.write permission (S156-04) — granted only to the SHANKPIT460-SERVER M2M agent, not to player JWTs, since this endpoint trusts the request body with no server-side verification the numbers are real.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"kills":  map[string]any{"type": "integer"},
									"deaths": map[string]any{"type": "integer"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Updated", "content": map[string]any{
						"application/json": map[string]any{"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"updated":      map[string]any{"type": "boolean"},
								"kills_added":  map[string]any{"type": "integer"},
								"deaths_added": map[string]any{"type": "integer"},
							},
						}},
					}},
					"403": errorResponse("Missing shankpit.match.write permission"),
					"404": errorResponse("Player not found"),
				},
			},
		},
		// ── SHANKPIT auth (email + Google OAuth) ────────────────────────────────
		"/api/v1/auth/email/register": map[string]any{
			"post": map[string]any{
				"summary":  "Register a SHANKPIT player with email + password",
				"tags":     []string{"shankpit"},
				"security": []map[string]any{},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"email", "password"},
								"properties": map[string]any{
									"email":    map[string]any{"type": "string", "format": "email"},
									"password": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Player created", "content": map[string]any{
						"application/json": map[string]any{"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"player_id":    map[string]any{"type": "string"},
								"display_name": map[string]any{"type": "string"},
								"token":        map[string]any{"type": "string", "description": "IDUNA JWT"},
							},
						}},
					}},
					"409": errorResponse("Email already registered"),
				},
			},
		},
		"/api/v1/auth/email/login": map[string]any{
			"post": map[string]any{
				"summary":  "Log in a SHANKPIT player with email + password",
				"tags":     []string{"shankpit"},
				"security": []map[string]any{},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"email", "password"},
								"properties": map[string]any{
									"email":    map[string]any{"type": "string", "format": "email"},
									"password": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Login OK", "content": map[string]any{
						"application/json": map[string]any{"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"player_id":    map[string]any{"type": "string"},
								"display_name": map[string]any{"type": "string"},
								"token":        map[string]any{"type": "string", "description": "IDUNA JWT"},
							},
						}},
					}},
					"401": errorResponse("Wrong email/password"),
				},
			},
		},
		"/api/v1/auth/google/shankpit": map[string]any{
			"get": map[string]any{
				"summary":     "Start SHANKPIT's Google OAuth browser flow",
				"tags":        []string{"shankpit"},
				"description": "Redirects to Google; on success redirects back to shankpit://auth?token=... (the callback below).",
				"security":    []map[string]any{},
				"responses": map[string]any{
					"302": map[string]any{"description": "Redirect to Google's OAuth consent screen"},
				},
			},
		},
		"/api/v1/auth/google/shankpit/callback": map[string]any{
			"get": map[string]any{
				"summary":  "Google OAuth callback for SHANKPIT — issues an IDUNA JWT",
				"tags":     []string{"shankpit"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"302": map[string]any{"description": "Redirect to shankpit://auth?token=<jwt>"},
					"400": errorResponse("OAuth exchange failed"),
				},
			},
		},
		// ── SHANKPIT-460 connect ticket + matchmaking (S156-02/03) ──────────────
		"/api/v1/shankpit/ticket": map[string]any{
			"post": map[string]any{
				"summary":     "Mint a short-lived HMAC connect ticket for the shankpit-460 game server",
				"tags":        []string{"shankpit"},
				"description": "The caller's JWT subject must be a player_id UUID. Ticket is a 5-minute HMAC-SHA256-signed token (player_id + expiry + truncated MAC over SHANKPIT_TICKET_SECRET) the C game server verifies locally on PACKET_CONNECT, with no crypto library and no I/O on the C side.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Ticket minted", "content": map[string]any{
						"application/json": map[string]any{"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"ticket":     map[string]any{"type": "string", "description": "72 hex chars: 16-byte player_id + 4-byte expiry + 16-byte truncated MAC"},
								"expires_at": map[string]any{"type": "integer", "description": "Unix timestamp"},
								"player_id":  map[string]any{"type": "string"},
							},
						}},
					}},
					"400": errorResponse("Token subject is not a player id"),
					"503": errorResponse("SHANKPIT_TICKET_SECRET not configured"),
				},
			},
		},
		"/api/v1/shankpit/queue/join": map[string]any{
			"post": map[string]any{
				"summary":     "Join the shankpit-460 v0 matchmaking queue",
				"tags":        []string{"shankpit"},
				"description": "In-process, ephemeral FIFO queue (S156-03). Once queuing players reach ShankpitQueueMinPlayers (2), everyone currently queuing is matched and given the one persistent game server's connect address.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Queue status", "content": jsonSchema("ShankpitQueueStatus")},
					"400": errorResponse("Token subject is not a player id"),
				},
			},
		},
		"/api/v1/shankpit/queue/leave": map[string]any{
			"post": map[string]any{
				"summary":  "Leave the shankpit-460 matchmaking queue",
				"tags":     []string{"shankpit"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Always not_queued", "content": jsonSchema("ShankpitQueueStatus")},
				},
			},
		},
		"/api/v1/shankpit/queue/status": map[string]any{
			"get": map[string]any{
				"summary":  "Poll the caller's current matchmaking queue status",
				"tags":     []string{"shankpit"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Queue status", "content": jsonSchema("ShankpitQueueStatus")},
				},
			},
		},
		// ── Blog (okemily.com) ───────────────────────────────────────────────────
		"/api/v1/blog/posts": map[string]any{
			"get": map[string]any{
				"summary":  "List blog posts (newest first)",
				"tags":     []string{"blog"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "Post index (no body field)"},
				},
			},
			"post": map[string]any{
				"summary":     "Publish a blog post",
				"tags":        []string{"blog"},
				"description": "Requires blog.write. Immediately re-renders that post + the index to static HTML — publishing is live the instant the request returns.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"slug", "title", "body"},
								"properties": map[string]any{
									"slug":   map[string]any{"type": "string", "description": "lowercase letters/numbers/hyphens"},
									"title":  map[string]any{"type": "string"},
									"author": map[string]any{"type": "string", "description": "defaults to EINHORN_INDUSTRIAL"},
									"body":   map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Published"},
					"400": errorResponse("Invalid slug/title/body"),
					"409": errorResponse("Slug already exists"),
				},
			},
		},
		"/api/v1/blog/posts/{slug}": map[string]any{
			"parameters": []map[string]any{
				{"name": "slug", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
			},
			"get": map[string]any{
				"summary":  "Get a single blog post with full body",
				"tags":     []string{"blog"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "Post"},
					"404": errorResponse("Not found"),
				},
			},
		},
		// ── Mailing list (okemily.com signup form) ──────────────────────────────
		"/api/v1/mailing-list/subscribe": map[string]any{
			"post": map[string]any{
				"summary":     "Subscribe an email to the okemily.com mailing list",
				"tags":        []string{"mailing-list"},
				"description": "Public, rate-limited 5/min/IP, CORS-scoped to okemily.com. Fails closed (503) while the vault is locked (see cmd/mailing-list-unlock).",
				"security":    []map[string]any{},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"email", "consent"},
								"properties": map[string]any{
									"email":   map[string]any{"type": "string", "format": "email"},
									"consent": map[string]any{"type": "boolean"},
									"list":    map[string]any{"type": "string", "description": "Optional dedicated signup list (e.g. \"stinkies\"). Omit for the general okemily.com list."},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Subscribed"},
					"400": errorResponse("Invalid email or consent not given"),
					"429": errorResponse("Rate limited"),
					"503": errorResponse("Vault locked")},
			},
		},
		// ── Status page ──────────────────────────────────────────────────────────
		"/api/v1/status": map[string]any{
			"get": map[string]any{
				"summary":     "Public system status page",
				"tags":        []string{"status"},
				"description": "Self-reported from the same host running these services, not independent third-party monitoring.",
				"security":    []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "Status per target + live 24h uptime percentage"},
				},
			},
		},
		// ── Check-in monitors ────────────────────────────────────────────────────
		"/api/v1/monitors/checkin/{slug}": map[string]any{
			"parameters": []map[string]any{
				{"name": "slug", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
			},
			"get": map[string]any{
				"summary":  "Record a monitor heartbeat (GET variant, for curl/wget probes)",
				"tags":     []string{"monitors"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "Checked in"},
					"404": errorResponse("Unknown monitor slug"),
				},
			},
			"post": map[string]any{
				"summary":  "Record a monitor heartbeat",
				"tags":     []string{"monitors"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "Checked in"},
					"404": errorResponse("Unknown monitor slug"),
				},
			},
		},
		"/api/v1/monitors": map[string]any{
			"get": map[string]any{
				"summary":  "List monitors (requires monitors.read or monitors.admin)",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Monitor list"},
				},
			},
			"post": map[string]any{
				"summary":  "Create a check-in monitor (requires monitors.create or monitors.admin)",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"name"},
								"properties": map[string]any{
									"name":                map[string]any{"type": "string"},
									"kind":                map[string]any{"type": "string", "enum": []string{"heartbeat", "cron", "deadman"}, "default": "heartbeat"},
									"timeout_seconds":     map[string]any{"type": "integer", "default": 3600},
									"grace_seconds":       map[string]any{"type": "integer", "default": 60},
									"alert_slack_channel": map[string]any{"type": "string"},
									"alert_email":         map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"201": map[string]any{"description": "Monitor created (includes generated slug and its own private check-in URL)"},
					"400": errorResponse("Missing name or invalid kind"),
				},
			},
		},
		"/api/v1/monitors/overdue": map[string]any{
			"get": map[string]any{
				"summary":  "List monitors currently overdue (requires monitors.alert or monitors.admin)",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Overdue monitor list"},
				},
			},
		},
		"/api/v1/monitors/{id}": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
			},
			"get": map[string]any{
				"summary":  "Get a single monitor",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Monitor"},
					"404": errorResponse("Not found"),
				},
			},
			"patch": map[string]any{
				"summary":  "Update a monitor",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Updated monitor"},
				},
			},
			"delete": map[string]any{
				"summary":  "Delete a monitor",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"204": map[string]any{"description": "Deleted"},
				},
			},
		},
		"/api/v1/monitors/{id}/alerted": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
			},
			"post": map[string]any{
				"summary":  "Mark a monitor's current overdue state as alerted (suppresses re-notification)",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Marked"},
				},
			},
		},
		"/api/v1/monitors/{id}/recover": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
			},
			"post": map[string]any{
				"summary":  "Clear a monitor's overdue/alerted state without waiting for a real check-in",
				"tags":     []string{"monitors"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Recovered"},
				},
			},
		},
		// ── Subscriptions (Emily+/GFD) ───────────────────────────────────────────
		"/api/v1/subscriptions": map[string]any{
			"post": map[string]any{
				"summary":     "Provision or update a subscription",
				"tags":        []string{"subscriptions"},
				"description": "Requires subscriptions.admin.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"user_id", "plan", "status"},
								"properties": map[string]any{
									"user_id":    map[string]any{"type": "string"},
									"plan":       map[string]any{"type": "string"},
									"status":     map[string]any{"type": "string"},
									"expires_at": map[string]any{"type": "string", "format": "date-time"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Provisioned"},
					"403": errorResponse("Missing subscriptions.admin"),
				},
			},
		},
		"/api/v1/subscriptions/me": map[string]any{
			"get": map[string]any{
				"summary":  "Get the caller's own subscription status",
				"tags":     []string{"subscriptions"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Subscription status"},
				},
			},
		},
		"/api/v1/subscriptions/tiers": map[string]any{
			"get": map[string]any{
				"summary":  "List available GFD subscription tiers",
				"tags":     []string{"subscriptions"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "Tier list"},
				},
			},
		},
		// ── Push tokens (MJOLNIR FCM) ────────────────────────────────────────────
		"/api/v1/push-tokens": map[string]any{
			"post": map[string]any{
				"summary":     "Register or update an FCM push token",
				"tags":        []string{"push-tokens"},
				"description": "Requires push_tokens.write.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"agent_name", "platform", "fcm_token"},
								"properties": map[string]any{
									"agent_name":  map[string]any{"type": "string"},
									"platform":    map[string]any{"type": "string", "example": "android"},
									"fcm_token":   map[string]any{"type": "string"},
									"fingerprint": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Token stored"},
					"403": errorResponse("Missing push_tokens.write"),
				},
			},
		},
		"/api/v1/push-tokens/{agent}": map[string]any{
			"parameters": []map[string]any{
				{"name": "agent", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
			},
			"get": map[string]any{
				"summary":     "Get the current push token for an agent",
				"tags":        []string{"push-tokens"},
				"description": "Requires push_tokens.read.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Token record"},
					"403": errorResponse("Missing push_tokens.read"),
					"404": errorResponse("No token registered for this agent"),
				},
			},
		},
		// ── Intelligence (MJOLNIR camera → Emily Prime vision) ───────────────────
		"/api/v1/intelligence/observe": map[string]any{
			"post": map[string]any{
				"summary":     "Submit an image for Emily Prime vision analysis",
				"tags":        []string{"intelligence"},
				"description": "Requires intelligence.observe.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"image_data"},
								"properties": map[string]any{
									"image_data": map[string]any{"type": "string", "description": "base64-encoded image"},
									"media_type": map[string]any{"type": "string", "default": "image/jpeg"},
									"prompt":     map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"201": map[string]any{"description": "Observation queued (status: pending)"},
					"403": errorResponse("Missing intelligence.observe")},
			},
		},
		"/api/v1/intelligence/observations": map[string]any{
			"get": map[string]any{
				"summary":     "List the caller's own observations",
				"tags":        []string{"intelligence"},
				"description": "Requires intelligence.read. Callers with apples.admin may pass ?agent_name= to see another agent's observations.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"parameters": []map[string]any{
					{"name": "status", "in": "query", "schema": map[string]any{"type": "string"}},
					{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 50}},
					{"name": "agent_name", "in": "query", "schema": map[string]any{"type": "string"}, "description": "requires apples.admin to use"},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Observation list"},
				},
			},
		},
		"/api/v1/intelligence/observations/{id}": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
			},
			"get": map[string]any{
				"summary":  "Get a single observation (own, or any with apples.admin)",
				"tags":     []string{"intelligence"},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{"description": "Observation detail"},
					"403": errorResponse("Not your observation"),
					"404": errorResponse("Not found"),
				},
			},
			"patch": map[string]any{
				"summary":     "Update an observation's analysis/status",
				"tags":        []string{"intelligence"},
				"description": "Requires intelligence.observe — in practice, only Emily Prime.",
				"security":    []map[string]any{{"bearerAuth": []string{}}},
				"requestBody": map[string]any{
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"analysis": map[string]any{"type": "string"},
									"apple_id": map[string]any{"type": "integer"},
									"status":   map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Updated observation"},
				},
			},
		},
		// NOTE: the DragonsNShit MMO API (/api/v1/characters, /items, /guilds,
		// /world-events, /fieldoffices) and /api/v1/supply, /api/v1/research,
		// /api/v1/kgraph are deliberately NOT documented here yet — internal/
		// experimental surfaces, not yet part of the public contract this
		// playground exists for. Disclosed gap, not an oversight; see
		// EMILY/BACKLOG.md SECTION 153 for the original stale-spec finding this
		// pass only partially closes.
		// ── Event stream ──────────────────────────────────────────────────────
		"/api/v1/stream/user-events": map[string]any{
			"get": map[string]any{
				"summary":     "SSE stream of user-event log records",
				"tags":        []string{"stream"},
				"description": "Server-Sent Events stream. Events are emitted as they are appended to the user-event log. Colab notebooks and Distributed Emily clusters subscribe here for real-time user lifecycle events.",
				"parameters": []map[string]any{
					{"name": "from_seq", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1}, "description": "First sequence number to stream (default 1 = full replay)"},
					{"name": "timeout", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600}, "description": "Seconds to hold the connection open (default 300, max 3600)"},
				},
				"security": []map[string]any{{"bearerAuth": []string{}}},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "SSE stream (text/event-stream). Event types: user_event, error, eof.",
						"content": map[string]any{
							"text/event-stream": map[string]any{
								"schema": map[string]any{"type": "string"},
							},
						},
					},
					"401": errorResponse("Unauthorized"),
				},
			},
		},
		// ── Drive ─────────────────────────────────────────────────────────────
		"/api/v1/drive/upload": map[string]any{
			"post": map[string]any{
				"summary":     "Upload file to Google Drive",
				"tags":        []string{"drive"},
				"description": "Multipart form upload. Requires drive.write permission.",
				"requestBody": map[string]any{
					"content": map[string]any{
						"multipart/form-data": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"file":      map[string]any{"type": "string", "format": "binary"},
									"filename":  map[string]any{"type": "string"},
									"mime_type": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "File metadata", "content": jsonSchema("DriveFile")},
					"503": errorResponse("Drive not configured"),
				},
			},
		},
		"/api/v1/drive/files": map[string]any{
			"get": map[string]any{
				"summary": "List Drive files",
				"tags":    []string{"drive"},
				"responses": map[string]any{
					"200": map[string]any{"description": "File list", "content": jsonSchemaArray("DriveFile")},
					"503": errorResponse("Drive not configured"),
				},
			},
		},
		// ── HEIMDAL ───────────────────────────────────────────────────────────
		"/api/v1/heimdal/sprints": map[string]any{
			"post": map[string]any{
				"summary": "Create HEIMDAL sprint item",
				"tags":    []string{"heimdal"},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"required": []string{"requirement"},
								"properties": map[string]any{
									"requirement": map[string]any{"type": "string"},
									"agent_name":  map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"201": map[string]any{"description": "Sprint created", "content": jsonSchema("SprintItem")},
				},
			},
			"get": map[string]any{
				"summary": "List HEIMDAL sprints",
				"tags":    []string{"heimdal"},
				"parameters": []map[string]any{
					{"name": "agent_name", "in": "query", "schema": map[string]any{"type": "string"}},
					{"name": "status", "in": "query", "schema": map[string]any{"type": "string"}},
					{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 20}},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Sprint list", "content": jsonSchemaArray("SprintItem")},
				},
			},
		},
		"/api/v1/heimdal/sprints/{id}": map[string]any{
			"parameters": []map[string]any{
				{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}},
			},
			"get": map[string]any{
				"summary": "Get single HEIMDAL sprint",
				"tags":    []string{"heimdal"},
				"responses": map[string]any{
					"200": map[string]any{"description": "Sprint", "content": jsonSchema("SprintItem")},
					"404": errorResponse("Not found"),
				},
			},
			"patch": map[string]any{
				"summary": "Update HEIMDAL sprint status",
				"tags":    []string{"heimdal"},
				"requestBody": map[string]any{
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"status":     map[string]any{"type": "string"},
									"roadmap_id": map[string]any{"type": "string"},
									"apple_id":   map[string]any{"type": "integer"},
									"criteria":   map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Updated sprint", "content": jsonSchema("SprintItem")},
				},
			},
		},
		// ── Health ────────────────────────────────────────────────────────────
		"/health": map[string]any{
			"get": map[string]any{
				"summary":  "Health check",
				"tags":     []string{"system"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "OK"},
				},
			},
		},
		"/.well-known/jwks.json": map[string]any{
			"get": map[string]any{
				"summary":  "JWKS — public keys for JWT verification",
				"tags":     []string{"system"},
				"security": []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "JWKS"},
				},
			},
		},
	},
}

// ── schema helpers ─────────────────────────────────────────────────────────

func jsonSchema(ref string) map[string]any {
	return map[string]any{
		"application/json": map[string]any{
			"schema": map[string]any{"$ref": "#/components/schemas/" + ref},
		},
	}
}

func jsonSchemaArray(ref string) map[string]any {
	return map[string]any{
		"application/json": map[string]any{
			"schema": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/components/schemas/" + ref},
			},
		},
	}
}

func errorResponse(desc string) map[string]any {
	return map[string]any{
		"description": desc,
		"content":     jsonSchema("ErrorResponse"),
	}
}
