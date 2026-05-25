package scanner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/FallseF/Kioku/internal/model"
)

// slashCommandRegex matches a leading slash command like "/foo", "/foo-bar",
// or "/plugin:foo-bar" optionally followed by arguments.
var slashCommandRegex = regexp.MustCompile(`^/[a-zA-Z][a-zA-Z0-9_:-]*(\s+(.*))?$`)

type rawEntry struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	GitBranch string          `json:"gitBranch"`
	AITitle   string          `json:"aiTitle"`
	Message   *rawMessage     `json:"message,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ProjectsDir returns the Claude Code projects directory.
func ProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Options controls what ScanAll includes.
type Options struct {
	// IncludeBackground includes observer/background sessions (claude-mem
	// observer-sessions, etc.) which are usually noise.
	IncludeBackground bool
}

// ScanAll walks the projects directory and returns sessions, newest first.
// By default, claude-mem observer sessions are filtered out.
func ScanAll() ([]model.Session, error) {
	return ScanAllWithOptions(Options{})
}

func ScanAllWithOptions(opts Options) ([]model.Session, error) {
	root, err := ProjectsDir()
	if err != nil {
		return nil, err
	}

	var sessions []model.Session
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, dirEntry := range entries {
		if !dirEntry.IsDir() {
			continue
		}
		if !opts.IncludeBackground && isBackgroundDir(dirEntry.Name()) {
			continue
		}
		projectDir := filepath.Join(root, dirEntry.Name())
		files, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			full := filepath.Join(projectDir, f.Name())
			s, err := parseSession(full)
			if err != nil {
				continue
			}
			sessions = append(sessions, s)
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastUpdated.After(sessions[j].LastUpdated)
	})
	return sessions, nil
}

// isBackgroundDir returns true for project directories that hold non-user
// background/observer sessions (currently: claude-mem observer-sessions).
func isBackgroundDir(name string) bool {
	return strings.Contains(name, "claude-mem") && strings.Contains(name, "observer-sessions")
}

func parseSession(path string) (model.Session, error) {
	s := model.Session{FilePath: path}

	info, err := os.Stat(path)
	if err != nil {
		return s, err
	}
	s.LastUpdated = info.ModTime()
	s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")

	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)

	count := 0
	for scanner.Scan() {
		count++
		var e rawEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if s.CWD == "" && e.CWD != "" {
			s.CWD = e.CWD
		}
		if s.GitBranch == "" && e.GitBranch != "" {
			s.GitBranch = e.GitBranch
		}
		if s.Title == "" && e.Type == "ai-title" && e.AITitle != "" {
			s.Title = e.AITitle
		}
		if s.FirstMessage == "" && e.Type == "user" && e.Message != nil {
			text := extractUserText(e.Message.Content)
			if cleaned := cleanFirstMessage(text); cleaned != "" {
				s.FirstMessage = cleaned
			}
		}
		if e.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
				if t.After(s.LastUpdated) {
					s.LastUpdated = t
				}
			}
		}
	}
	s.MessageCount = count

	if s.CWD == "" {
		s.CWD = decodeCWDFromDir(filepath.Base(filepath.Dir(path)))
	}
	if s.Title == "" {
		s.Title = s.FirstMessage
	}
	return s, nil
}

// cleanFirstMessage returns the user-meaningful portion of a first message,
// or "" if the message is boilerplate / a bare slash command that carries no
// useful summary on its own (caller should try the next user entry).
//
// Transformations:
//   - Skip auto-injected greetings ("Hello memory agent…").
//   - Skip the lone "Claude Code" session-start token.
//   - For "/foo args…", drop the command and keep "args…".
//   - For a bare "/foo" with no args, return "" so the next message is used.
func cleanFirstMessage(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	if strings.HasPrefix(t, "Hello memory agent") {
		return ""
	}
	switch t {
	case "Claude Code", "claude-code":
		return ""
	}
	if m := slashCommandRegex.FindStringSubmatch(t); m != nil {
		args := strings.TrimSpace(m[2])
		return args
	}
	return t
}

func extractUserText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(content, &asString); err == nil {
		return truncate(asString, 200)
	}
	var asArray []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &asArray); err == nil {
		for _, item := range asArray {
			if item.Text != "" {
				return truncate(item.Text, 200)
			}
		}
	}
	return ""
}

// decodeCWDFromDir converts "-Users-alice-Documents" back to "/Users/alice/Documents".
// This is a best-effort fallback; Claude Code's actual encoding may not be losslessly
// reversible (paths containing '-' lose the distinction between '-' and '/').
func decodeCWDFromDir(name string) string {
	if !strings.HasPrefix(name, "-") {
		return name
	}
	return strings.ReplaceAll(name, "-", "/")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
