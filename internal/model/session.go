package model

import "time"

type Session struct {
	ID           string
	CWD          string
	Title        string
	FirstMessage string
	LastUpdated  time.Time
	MessageCount int
	GitBranch    string
	FilePath     string
}
