package auth

import "time"

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
