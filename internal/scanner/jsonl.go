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
	"unicode"

	"github.com/FallseF/Kioku/internal/model"
)

// noiseTags are harness/wrapper blocks that carry no user intent. Each is
// stripped whole (open tag through matching close), because merely deleting the
// angle brackets would leave the inner payload (tool-event JSON, etc.) as
// garbage. RE2 has no backreferences, so we compile one regex per tag.
var noiseTags = []string{
	"command-message", "command-name", "command-args",
	"local-command-stdout", "local-command-caveat", "system-reminder",
	"observed_from_primary_session", "task-notification",
}

var (
	noiseBlockRegexes = compileBlockRegexes(noiseTags)

	// anyTagRegex mops up any stray XML-ish tag fragment left after the block
	// strip. It requires a letter (or '/') right after '<' and forbids newlines,
	// so user text like "a < b and c > d" or multi-line code is NOT eaten —
	// only things that actually look like a tag on a single line.
	anyTagRegex = regexp.MustCompile(`</?[A-Za-z][^>\n]*>`)

	whitespaceRegex = regexp.MustCompile(`\s+`)

	// cmdNameRegex / cmdArgsRegex recover a readable "/name args" label from a
	// slash-command invocation that Claude Code wraps in <command-*> tags.
	cmdNameRegex = regexp.MustCompile(`(?is)<command-name>\s*(.*?)\s*</command-name>`)
	cmdArgsRegex = regexp.MustCompile(`(?is)<command-args>\s*(.*?)\s*</command-args>`)
)

func compileBlockRegexes(tags []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(tags))
	for i, t := range tags {
		out[i] = regexp.MustCompile(`(?is)<` + t + `>.*?</` + t + `>`)
	}
	return out
}

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
	// IncludeBackground includes observer/background/subagent sessions
	// (claude-mem observer-sessions, agent-* subagent transcripts, autocomplete
	// suggestion sessions) which are usually noise rather than resumable work.
	IncludeBackground bool
}

// ScanAll walks the projects directory and returns sessions, newest first.
// By default, background/observer/subagent sessions are filtered out.
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
			if s.Background && !opts.IncludeBackground {
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

// genContextParts is how many cleaned user messages we stitch together as
// context for the local title generator.
const genContextParts = 3

func parseSession(path string) (model.Session, error) {
	s := model.Session{FilePath: path}

	info, err := os.Stat(path)
	if err != nil {
		return s, err
	}
	s.LastUpdated = info.ModTime()
	s.FileModTime = info.ModTime()
	s.Size = info.Size()
	s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")

	// agent-* files are subagent transcripts, not resumable top-level sessions.
	if strings.HasPrefix(s.ID, "agent-") {
		s.Background = true
	}

	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)

	count := 0
	sawUser := false
	var ctxParts []string
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
		if s.Title == "" && e.Type == "ai-title" && e.AITitle != "" && !isJunkAITitle(e.AITitle) {
			s.Title = e.AITitle
		}
		if e.Type == "user" && e.Message != nil {
			text := extractUserText(e.Message.Content)
			if !sawUser {
				sawUser = true
				if isSuggestionNoise(text) || isObserverSession(text) {
					s.Background = true
				}
			}
			if len(ctxParts) < genContextParts {
				if cleaned := cleanFirstMessage(text); cleaned != "" {
					ctxParts = append(ctxParts, cleaned)
				}
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

	if len(ctxParts) > 0 {
		// FirstMessage is only used for the headline and filter text; cap it so a
		// single huge message can't bloat memory across thousands of sessions.
		s.FirstMessage = truncate(ctxParts[0], 300)
		s.GenContext = truncate(strings.Join(ctxParts, " / "), 1200)
	}
	if s.CWD == "" {
		s.CWD = decodeCWDFromDir(filepath.Base(filepath.Dir(path)))
	}
	// s.Title holds Claude Code's aiTitle and stays empty when there is none, so
	// the UI can tell a real aiTitle apart from the user's first message.
	return s, nil
}

// isSuggestionNoise reports whether a session's very first user message is an
// autocomplete/suggestion harness prompt rather than a real human turn.
func isSuggestionNoise(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "[SUGGESTION MODE")
}

// isObserverSession reports whether a session is a claude-mem observer run
// (which only watches another session and holds no resumable user work),
// identified by its <observed_from_primary_session> signature regardless of the
// project directory name.
func isObserverSession(text string) bool {
	return strings.Contains(text, "<observed_from_primary_session") ||
		strings.Contains(text, "you are continuing to observe the primary")
}

// isJunkAITitle reports whether an aiTitle is a harness-generated placeholder
// (e.g. "Resume coding session <uuid>") rather than a real topic title.
func isJunkAITitle(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "Resume coding session")
}

// cleanFirstMessage returns the user-meaningful portion of a message, or "" if
// the message is harness boilerplate that carries no useful summary on its own
// (caller should then try the next user entry).
func cleanFirstMessage(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}

	// claude-mem memory/observer wrapper: the real human turn is nested inside
	// <user_request>; everything around it is injected. Recurse on the inner
	// text so we recover the intent instead of discarding the whole message.
	if strings.HasPrefix(t, "Hello memory agent") || strings.Contains(t, "<observed_from_primary_session") {
		if inner := extractTagInner(t, "user_request"); inner != "" {
			return cleanFirstMessage(inner)
		}
		return ""
	}

	// Slash-command invocation wrapped in <command-*> tags -> "/name args".
	// Pure control commands (/clear, /effort, …) carry no topic, so we skip
	// them and let the next message become the headline.
	if label := extractCommandLabel(t); label != "" {
		if isControlCommand(label) {
			return ""
		}
		return label
	}

	for _, re := range noiseBlockRegexes {
		t = re.ReplaceAllString(t, " ")
	}
	t = anyTagRegex.ReplaceAllString(t, " ")
	t = strings.TrimSpace(whitespaceRegex.ReplaceAllString(t, " "))
	if t == "" || isBoilerplate(t) || isControlCommand(t) || !hasMeaningfulRune(t) {
		return ""
	}
	return t
}

// controlCommands are slash commands that drive the harness rather than
// describe work; they make useless headlines and are skipped.
var controlCommands = map[string]bool{
	"clear": true, "compact": true, "effort": true, "cost": true,
	"config": true, "model": true, "resume": true, "exit": true,
	"quit": true, "help": true, "status": true, "doctor": true,
	"login": true, "logout": true, "vim": true, "terminal-setup": true,
	"bug": true, "release-notes": true, "memory": true, "fast": true,
	"export": true, "pr-comments": true,
}

// isControlCommand reports whether s is (or starts with) a control slash command.
func isControlCommand(s string) bool {
	if !strings.HasPrefix(s, "/") {
		return false
	}
	name := s[1:]
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	return controlCommands[name]
}

// extractTagInner returns the text between <tag> and </tag>, or "" if the open
// tag is absent. An unterminated tag yields everything after the open tag.
func extractTagInner(s, tag string) string {
	open, closeTag := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	if j := strings.Index(rest, closeTag); j >= 0 {
		return strings.TrimSpace(rest[:j])
	}
	return strings.TrimSpace(rest)
}

// extractCommandLabel recovers "/name args" from a slash-command invocation
// that Claude Code records as <command-name>/name</command-name> plus an
// optional <command-args>. Returns "" when the text has no command-name tag.
func extractCommandLabel(t string) string {
	m := cmdNameRegex.FindStringSubmatch(t)
	if m == nil {
		return ""
	}
	name := strings.TrimSpace(m[1])
	if name == "" {
		return ""
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	if a := cmdArgsRegex.FindStringSubmatch(t); a != nil {
		if args := strings.TrimSpace(a[1]); args != "" {
			return truncate(name+" "+args, 200)
		}
	}
	return name
}

// isBoilerplate reports whether cleaned text is a harness-generated prompt that
// should be skipped in favor of the next user message.
func isBoilerplate(t string) bool {
	low := strings.ToLower(t)
	switch {
	case strings.HasPrefix(t, "Hello memory agent"),
		t == "Claude Code", t == "claude-code",
		strings.HasPrefix(t, "PROGRESS SUMMARY CHECKPOINT"),
		strings.HasPrefix(t, "Resume coding session"),
		strings.HasPrefix(low, "your task is to create a detailed summary"),
		strings.HasPrefix(low, "this session is being continued"),
		strings.HasPrefix(t, "[SUGGESTION MODE"),
		strings.HasPrefix(t, "Caveat:"):
		return true
	}
	return false
}

// hasMeaningfulRune reports whether s contains at least one letter or digit, so
// pure separators ("=======") and punctuation are treated as empty.
func hasMeaningfulRune(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func extractUserText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// Cap generously (not tightly) so a large leading noise block keeps its
	// closing tag intact for block-stripping / <user_request> extraction; the
	// final headline is capped after cleaning instead.
	const rawCap = 65536
	var asString string
	if err := json.Unmarshal(content, &asString); err == nil {
		return truncate(asString, rawCap)
	}
	// Array content: keep only text blocks; tool_result/image blocks have no
	// Text field and are skipped.
	var asArray []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &asArray); err == nil {
		for _, item := range asArray {
			if item.Text != "" {
				return truncate(item.Text, rawCap)
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
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
