package auth

import "time"

// AppleRecord is a golden documentation entry from a self-improvement run.
// Append-only: records are never updated or deleted.
type AppleRecord struct {
	ID         int64
	AgentID    string
	SourceRepo string
	RunID      string
	AppleType  string
	Title      string
	Body       string
	Metadata   []byte    // raw JSON, may be nil
	RecordedAt time.Time
}

// PushToken is an FCM device token registered by a MJOLNIR Android client.
// Upserted on each app launch; agent_name identifies the device owner.
type PushToken struct {
	ID          int64
	AgentName   string
	Platform    string // "android"
	FCMToken    string
	Fingerprint string
	RegisteredAt time.Time
	LastUsedAt   time.Time
}

// Agent is a first-class IAM identity for automated systems.
type Agent struct {
	ID          string
	OwnerUserID string
	Name        string
	Type        string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Permissions []string
}

// Role is a named set of permissions in the RBAC system.
type Role struct {
	ID          string
	Name        string
	Description string
}

// IAMEvent is a record from the iam_event_stream audit log.
type IAMEvent struct {
	EventID       int64
	EventType     string
	AggregateType string
	AggregateID   string
	OperatorID    string
	Payload       []byte
	RecordedAt    time.Time
}
