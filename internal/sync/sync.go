// Package sync exports Claude Code sessions to a local Git clone of a
// user-provided private repository so they can be resumed on another machine.
package sync

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	gexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/FallseF/Kioku/internal/model"
)

// RepoPath returns the local clone of the sync repo. Override with
// KIOKU_SYNC_DIR if you keep the clone elsewhere; otherwise we default to the
// macOS app-support convention.
func RepoPath() string {
	if v := os.Getenv("KIOKU_SYNC_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "kioku", "sync")
}

// ErrNotConfigured is returned by Export when the sync repo isn't set up.
var ErrNotConfigured = fmt.Errorf("sync not configured")

// ConfigInstructions returns a multi-line guide for users hitting
// ErrNotConfigured. Friends can copy-paste these to enable gg.
func ConfigInstructions() string {
	return strings.Join([]string{
		"sync先のprivate repoを用意してね:",
		"  1. GitHub上に空のprivate repoを作成 (例: <yourname>/kioku-sync)",
		"  2. ローカルにcloneする:",
		"     mkdir -p \"$HOME/Library/Application Support/kioku\"",
		"     git clone https://github.com/<yourname>/kioku-sync.git \\",
		"       \"$HOME/Library/Application Support/kioku/sync\"",
		"  3. 別の場所にcloneしたい時は環境変数 KIOKU_SYNC_DIR を設定",
	}, "\n")
}

// Meta is the sidecar metadata written next to every exported session. It
// gives the receiving machine enough context to recognize a handoff.
type Meta struct {
	SchemaVersion  int       `json:"schemaVersion"`
	SessionID      string    `json:"sessionId"`
	SourceHostname string    `json:"sourceHostname"`
	SourceUser     string    `json:"sourceUser"`
	SourceCWD      string    `json:"sourceCwd"`
	SourceOS       string    `json:"sourceOs"`
	SourceArch     string    `json:"sourceArch"`
	ExportedAt     time.Time `json:"exportedAt"`
	ExportedBy     string    `json:"exportedBy"` // "cchist"
	MessageCount   int       `json:"messageCount"`
	Title          string    `json:"title,omitempty"`
	GitBranch      string    `json:"gitBranch,omitempty"`
}

// PreviewReport summarizes what `Export` will push, for display before the
// user confirms.
type PreviewReport struct {
	SessionPath  string
	MemoryDir    string // empty if not found
	CLAUDEMDPath string // empty if not found
	TotalBytes   int64
	FileCount    int
	TargetDir    string // sessions/<id> inside the sync repo
}

// Preview inspects what would be bundled without writing anything.
func Preview(s *model.Session) (*PreviewReport, error) {
	r := &PreviewReport{TargetDir: filepath.Join("sessions", s.ID)}

	if s.FilePath == "" {
		return nil, fmt.Errorf("session has no file path")
	}
	if fi, err := os.Stat(s.FilePath); err == nil {
		r.SessionPath = s.FilePath
		r.TotalBytes += fi.Size()
		r.FileCount++
	} else {
		return nil, fmt.Errorf("session jsonl missing: %w", err)
	}

	if dir := memoryDir(); dir != "" {
		if files, bytes, ok := walkStats(dir); ok {
			r.MemoryDir = dir
			r.TotalBytes += bytes
			r.FileCount += files
		}
	}

	if path := projectCLAUDEMD(s.CWD); path != "" {
		if fi, err := os.Stat(path); err == nil {
			r.CLAUDEMDPath = path
			r.TotalBytes += fi.Size()
			r.FileCount++
		}
	}

	return r, nil
}

// Export bundles the session under <RepoPath()>/sessions/<id>/, commits, and
// pushes. Returns the commit SHA on success.
//
// If the local sync clone is missing, returns ErrNotConfigured so callers can
// show ConfigInstructions() to the user.
func Export(s *model.Session) (string, error) {
	repo := RepoPath()
	if _, err := os.Stat(repo); err != nil {
		return "", ErrNotConfigured
	}

	target := filepath.Join(repo, "sessions", s.ID)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}

	if err := copyFile(s.FilePath, filepath.Join(target, "session.jsonl")); err != nil {
		return "", fmt.Errorf("copy session.jsonl: %w", err)
	}

	if dir := memoryDir(); dir != "" {
		dst := filepath.Join(target, "memory")
		if err := copyDir(dir, dst); err != nil {
			return "", fmt.Errorf("copy memory: %w", err)
		}
	}

	if path := projectCLAUDEMD(s.CWD); path != "" {
		if err := copyFile(path, filepath.Join(target, "CLAUDE.md")); err != nil {
			return "", fmt.Errorf("copy CLAUDE.md: %w", err)
		}
	}

	meta := buildMeta(s)
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(target, "meta.json"), metaBytes, 0o644); err != nil {
		return "", fmt.Errorf("write meta.json: %w", err)
	}

	return gitCommitAndPush(s.ID, meta)
}

func buildMeta(s *model.Session) Meta {
	host, _ := os.Hostname()
	return Meta{
		SchemaVersion:  1,
		SessionID:      s.ID,
		SourceHostname: host,
		SourceUser:     os.Getenv("USER"),
		SourceCWD:      s.CWD,
		SourceOS:       runtime.GOOS,
		SourceArch:     runtime.GOARCH,
		ExportedAt:     time.Now().UTC(),
		ExportedBy:     "kioku",
		MessageCount:   s.MessageCount,
		Title:          s.Title,
		GitBranch:      s.GitBranch,
	}
}

func gitCommitAndPush(sessionID string, meta Meta) (string, error) {
	repo := RepoPath()
	run := func(args ...string) error {
		cmd := gexec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if err := run("add", filepath.Join("sessions", sessionID)); err != nil {
		return "", err
	}

	statusOut, err := gexec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(string(statusOut))) == 0 {
		return "", nil // nothing new to commit
	}

	msg := fmt.Sprintf("session %s from %s@%s\n\nexported_at: %s\ncwd: %s\nmessages: %d",
		sessionID[:8], meta.SourceUser, meta.SourceHostname,
		meta.ExportedAt.Format(time.RFC3339), meta.SourceCWD, meta.MessageCount)
	if err := run("-c", "commit.gpgsign=false", "commit", "-m", msg); err != nil {
		return "", err
	}

	shaOut, err := gexec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(shaOut))

	if err := run("push"); err != nil {
		return sha, fmt.Errorf("push failed (commit %s stays local): %w", sha[:8], err)
	}
	return sha, nil
}

func memoryDir() string {
	candidate := filepath.Join(os.Getenv("HOME"), ".claude", "projects", "-Users-"+filepathBaseUser(), "memory")
	if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
		return candidate
	}
	return ""
}

func filepathBaseUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return filepath.Base(os.Getenv("HOME"))
}

func projectCLAUDEMD(cwd string) string {
	for dir := cwd; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		p := filepath.Join(dir, "CLAUDE.md")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func walkStats(root string) (files int, bytes int64, ok bool) {
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err == nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target)
	})
}
