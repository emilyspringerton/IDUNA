package store

import (
	"context"

	"iduna/internal/auth"
)

// IAMStore defines the persistence interface for IDUNA IAM operations.
type IAMStore interface {
	// GetOrCreateUserByGoogleSubject looks up a user by google_subject.
	// If the user does not exist, it creates one with PENDING status.
	// The bool return value is true when the user was newly created.
	GetOrCreateUserByGoogleSubject(ctx context.Context, googleSub, email string) (*auth.User, bool, error)

	// GetUserByID returns a user by their string UUID.
	GetUserByID(ctx context.Context, id string) (*auth.User, error)

	// GetEffectivePermissions returns the distinct set of permission names
	// granted to the user through their assigned roles, sorted lexicographically.
	GetEffectivePermissions(ctx context.Context, userID string) ([]string, error)

	// AppendIAMEvent inserts a row into iam_event_stream.
	AppendIAMEvent(ctx context.Context, eventType, aggregateType, aggregateID, operatorID string, payload []byte) error

	// UpdateUserStatus changes the user's status and emits a corresponding IAM event.
	UpdateUserStatus(ctx context.Context, userID, status, operatorID string) error

	// --- Admin operations ---

	// ListUsers returns up to limit users ordered by created_at desc.
	ListUsers(ctx context.Context, limit int) ([]auth.User, error)

	// AssignRole grants a role to a user, emitting a RoleAssigned event.
	AssignRole(ctx context.Context, userID, roleID, operatorID string) error

	// RevokeRole removes a role from a user, emitting a RoleRevoked event.
	RevokeRole(ctx context.Context, userID, roleID, operatorID string) error

	// ListRoles returns all defined roles.
	ListRoles(ctx context.Context) ([]auth.Role, error)

	// ListAgents returns all agents ordered by created_at desc.
	ListAgents(ctx context.Context) ([]auth.Agent, error)

	// CreateAgent inserts a new agent and emits an AgentCreated event.
	CreateAgent(ctx context.Context, ownerUserID, name, agentType, operatorID string) (*auth.Agent, error)

	// UpdateAgentStatus changes an agent's status and emits an event.
	UpdateAgentStatus(ctx context.Context, agentID, status, operatorID string) error

	// ListIAMEvents returns the most recent limit events from iam_event_stream.
	ListIAMEvents(ctx context.Context, limit int) ([]auth.IAMEvent, error)
}
