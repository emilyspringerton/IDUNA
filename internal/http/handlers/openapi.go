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
