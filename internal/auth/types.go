package auth

import "time"

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
