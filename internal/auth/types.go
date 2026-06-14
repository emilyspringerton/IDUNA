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
