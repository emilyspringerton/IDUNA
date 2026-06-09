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

	// --- Agent M2M credentials (spec HQ-SPEC-IAM-095 §3.1) ---

	// SetAgentCredential stores a bcrypt hash of the given plaintext secret for the
	// agent. The previous credential (if any) is replaced. operatorID is recorded in
	// the resulting IAM event for audit.
	SetAgentCredential(ctx context.Context, agentID, plaintextSecret, operatorID string) error

	// AuthenticateAgent looks up an agent by name, verifies the plaintext secret
	// against its stored bcrypt hash, and returns the agent with its effective
	// permissions. Returns a non-nil error when authentication fails (name not found,
	// no credential set, wrong secret, or agent not ACTIVE).
	AuthenticateAgent(ctx context.Context, agentName, plaintextSecret string) (*auth.Agent, error)

	// ListIAMEvents returns the most recent limit events from iam_event_stream.
	ListIAMEvents(ctx context.Context, limit int) ([]auth.IAMEvent, error)

	// --- Apples (HQ-SPEC-IAM-096) ---

	// AppendApple inserts a golden documentation record and emits ApplePublished to iam_event_stream.
	AppendApple(ctx context.Context, apple auth.AppleRecord) (id int64, err error)

	// ListApples returns up to limit apples, most recent first.
	// Pass empty strings to omit a filter; pass 0 for limit to use the default (50).
	ListApples(ctx context.Context, agentID, sourceRepo, appleType string, limit int) ([]auth.AppleRecord, error)

	// GetApple returns a single apple by its integer ID.
	GetApple(ctx context.Context, id int64) (*auth.AppleRecord, error)

	// --- Push tokens (MJOLNIR FCM — HQ-SPEC-IAM-097) ---

	// UpsertPushToken inserts or updates an FCM device token for the given agent.
	// Deduplication key is (agent_name, fingerprint).
	UpsertPushToken(ctx context.Context, token auth.PushToken) error

	// GetPushToken returns the most recently registered FCM token for agent_name.
	// Returns nil, nil if no token is registered.
	GetPushToken(ctx context.Context, agentName string) (*auth.PushToken, error)

	// --- Camera observations (MJOLNIR intelligence — HQ-SPEC-IAM-098) ---

	// CreateCameraObservation inserts a new pending observation and returns its ID.
	CreateCameraObservation(ctx context.Context, obs auth.CameraObservation) (int64, error)

	// UpdateCameraObservation sets analysis, apple_id, status, and processed_at for an observation.
	UpdateCameraObservation(ctx context.Context, id int64, analysis string, appleID int64, status string) error

	// GetCameraObservation returns a single observation by ID.
	GetCameraObservation(ctx context.Context, id int64) (*auth.CameraObservation, error)

	// ListCameraObservations returns up to limit observations for agentName.
	// Pass status="" to return all statuses; otherwise filters by status.
	// Returns newest first.
	ListCameraObservations(ctx context.Context, agentName, status string, limit int) ([]auth.CameraObservation, error)

	// --- HEIMDAL sprints (sprint planning interface — HQ-SPEC-IAM-099) ---

	// CreateSprintItem inserts a new pending sprint and returns its ID.
	CreateSprintItem(ctx context.Context, item auth.SprintItem) (int64, error)

	// UpdateSprintItem sets criteria, roadmapID, status, apple_id, and updated_at.
	UpdateSprintItem(ctx context.Context, id int64, criteriaJSON, roadmapID, status string, appleID int64) error

	// GetSprintItem returns a single sprint by ID.
	GetSprintItem(ctx context.Context, id int64) (*auth.SprintItem, error)

	// ListSprintItems returns up to limit sprints. Pass agentName="" for all agents.
	// Pass status="" to return all statuses. Returns newest first.
	ListSprintItems(ctx context.Context, agentName, status string, limit int) ([]auth.SprintItem, error)
}
