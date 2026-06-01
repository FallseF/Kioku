package model

import "time"

type Session struct {
	ID           string
	CWD          string
	Title        string // Claude Code's own aiTitle (often English)
	FirstMessage string // first user message, cleaned of harness boilerplate
	GenContext   string // text fed to the local title generator; not displayed
	GenTitle     string // locally generated Japanese title (from ollama, cached)
	LastUpdated  time.Time
	FileModTime  time.Time // raw file mtime, used as a title-cache invalidation key
	Size         int64     // file size, used as a title-cache invalidation key
	MessageCount int
	GitBranch    string
	Background   bool // autocomplete/suggestion noise; hidden unless --all
	FilePath     string
}
