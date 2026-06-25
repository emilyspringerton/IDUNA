package auth

import "time"

// DailyTokenStat is one day's aggregate token usage extracted from Apple metadata.
type DailyTokenStat struct {
	Date   string `json:"date"`   // "YYYY-MM-DD" UTC
	Tokens int64  `json:"tokens"` // sum of metadata.tokens_used for all Apples that day
}

// Subscription tracks a user's Emily+ plan status.
type Subscription struct {
	ID        int64
	UserID    string
	Plan      string    // "emily_plus"
	Status    string    // "active" | "cancelled" | "expired"
	ExpiresAt time.Time // zero value = perpetual
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsActive returns true if the subscription grants cap.query.full.
func (s *Subscription) IsActive() bool {
	if s == nil || s.Status != "active" {
		return false
	}
	return s.ExpiresAt.IsZero() || time.Now().UTC().Before(s.ExpiresAt)
}

// Monitor is a check-in (heartbeat) monitor. Services POST to its unique
// check-in URL to confirm they are alive; if they don't within the timeout
// window, the alerting worker fires Slack/email notifications.
type Monitor struct {
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	Slug               string     `json:"slug"` // unique token in check-in URL
	TimeoutSeconds     int        `json:"timeout_seconds"`
	GraceSeconds       int        `json:"grace_seconds"`
	Owner              string     `json:"owner"` // agent_name or user_id
	LastCheckinAt      *time.Time `json:"last_checkin_at,omitempty"`
	AlertedAt          *time.Time `json:"alerted_at,omitempty"`
	AlertSlackChannel  string     `json:"alert_slack_channel,omitempty"`
	AlertEmail         string     `json:"alert_email,omitempty"`
	Status             string     `json:"status"` // unknown | healthy | failing
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// IsOverdue returns true when the monitor has exceeded its timeout+grace window.
func (m *Monitor) IsOverdue(now time.Time) bool {
	if m.LastCheckinAt == nil {
		// Never checked in — overdue after timeout+grace from creation.
		deadline := m.CreatedAt.Add(time.Duration(m.TimeoutSeconds+m.GraceSeconds) * time.Second)
		return now.After(deadline)
	}
	deadline := m.LastCheckinAt.Add(time.Duration(m.TimeoutSeconds+m.GraceSeconds) * time.Second)
	return now.After(deadline)
}

type User struct {
	ID                 [16]byte
	IDString           string // UUID string, set by IAM store; takes precedence when non-empty
	Handle             string
	Email              string
	Permissions        []string
	Status             string
	Roles              []string
	HonorAccepted      bool
	HonorCurrentSHA    string
	HonorCurrentVer    int
	HonorCurrentText   string
	HonorAcceptedAtUTC *time.Time
}
