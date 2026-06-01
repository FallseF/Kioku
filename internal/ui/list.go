package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/FallseF/Kioku/internal/model"
	"github.com/FallseF/Kioku/internal/titler"
)

const (
	chordTimeout = 1500 * time.Millisecond

	// headlineLimit is the rune length above which the user's first message is
	// considered too long to be a clean headline; such sessions fall through to
	// aiTitle or a locally generated title.
	headlineLimit = 64
)

type sessionItem struct {
	s model.Session
}

func (i sessionItem) FilterValue() string {
	return strings.Join([]string{i.s.FirstMessage, i.s.GenTitle, i.s.Title, i.s.CWD}, " ")
}

// Title is the headline shown for a session, resolved in priority order:
//  1. the user's own first message, when short enough to read at a glance;
//  2. a locally generated Japanese title (ollama), if present;
//  3. Claude Code's own aiTitle;
//  4. the long first message, truncated;
//  5. a placeholder.
//
// Generation (step 2) only ever runs for sessions that would otherwise land on
// step 4, so a short first message or an existing aiTitle always wins.
func (i sessionItem) Title() string {
	fm := strings.TrimSpace(i.s.FirstMessage)
	if fm != "" && runeLen(fm) <= headlineLimit {
		return fm
	}
	if i.s.GenTitle != "" {
		return i.s.GenTitle
	}
	if i.s.Title != "" {
		return i.s.Title
	}
	if fm != "" {
		return truncateRunes(fm, headlineLimit)
	}
	return "(無題)"
}

func (i sessionItem) Description() string {
	cwd := shortenPath(i.s.CWD)
	age := humanizeDuration(time.Since(i.s.LastUpdated))
	return fmt.Sprintf("%s  ·  %s  ·  %d件", cwd, age, i.s.MessageCount)
}

// NeedsTitleGen reports whether a session has no good headline yet and would
// benefit from a locally generated title.
func NeedsTitleGen(s model.Session) bool {
	if s.GenTitle != "" {
		return false
	}
	if fm := strings.TrimSpace(s.FirstMessage); fm != "" && runeLen(fm) <= headlineLimit {
		return false
	}
	if s.Title != "" {
		return false
	}
	return strings.TrimSpace(s.GenContext) != ""
}

type Model struct {
	list        list.Model
	sessions    []model.Session
	idIndex     map[string]int // sessionID -> index into sessions/items (stable, insertion order)
	selected    *model.Session
	width       int
	height      int
	copyChordAt time.Time // when the first 'c' of a "cc" chord was pressed; zero = not pending
	syncChordAt time.Time // when the first 'g' of a "gg" chord was pressed; zero = not pending

	gen      *titler.Generator
	cache    *titler.Cache
	titleCh  chan titleResult
	stop     chan struct{}
	stopOnce *sync.Once
}

// syncDoneMsg is the result of an asynchronous sync.Export call.
type syncDoneMsg struct {
	sha string
	err error
}

// titleResult carries one freshly generated title back to the Update loop.
type titleResult struct {
	id    string
	title string
}

// titlesDoneMsg signals the background generator has finished or been stopped.
type titlesDoneMsg struct{}

// NewModel builds the TUI. gen may be nil (ollama unavailable / disabled), in
// which case no titles are generated and cached/aiTitle headlines are used as
// is. cache is always provided so generated titles persist across runs.
func NewModel(sessions []model.Session, gen *titler.Generator, cache *titler.Cache) Model {
	items := make([]list.Item, len(sessions))
	idIndex := make(map[string]int, len(sessions))
	for i, s := range sessions {
		items[i] = sessionItem{s: s}
		idIndex[s.ID] = i
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("212")).
		BorderLeftForeground(lipgloss.Color("212"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("212")).
		BorderLeftForeground(lipgloss.Color("212"))

	l := list.New(items, delegate, 0, 0)
	l.Title = "Kioku"
	l.Styles.Title = lipgloss.NewStyle().
		Background(lipgloss.Color("57")).
		Foreground(lipgloss.Color("230")).
		Padding(0, 1)
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.SetStatusBarItemName("セッション", "セッション")
	l.StatusMessageLifetime = 5 * time.Second
	l.FilterInput.Prompt = "絞り込み: "
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "再開")),
			key.NewBinding(key.WithKeys("c"), key.WithHelp("cc", "コマンドをコピー")),
			key.NewBinding(key.WithKeys("g"), key.WithHelp("gg", "別PCへ同期")),
		}
	}

	km := l.KeyMap
	km.CursorUp.SetHelp("↑/k", "上へ")
	km.CursorDown.SetHelp("↓/j", "下へ")
	km.NextPage.SetHelp("→/l/pgdn", "次ページ")
	km.PrevPage.SetHelp("←/h/pgup", "前ページ")
	km.GoToStart.SetHelp("g/home", "先頭")
	km.GoToEnd.SetHelp("G/end", "末尾")
	km.Filter.SetHelp("/", "絞り込み")
	km.ClearFilter.SetHelp("esc", "絞り込み解除")
	km.CancelWhileFiltering.SetHelp("esc", "キャンセル")
	km.AcceptWhileFiltering.SetHelp("enter", "確定")
	km.ShowFullHelp.SetHelp("?", "詳細ヘルプ")
	km.CloseFullHelp.SetHelp("?", "ヘルプを閉じる")
	km.Quit.SetHelp("q", "終了")
	km.ForceQuit.SetHelp("ctrl+c", "強制終了")
	l.KeyMap = km

	m := Model{
		list:     l,
		sessions: sessions,
		idIndex:  idIndex,
		gen:      gen,
		cache:    cache,
		stopOnce: &sync.Once{},
	}
	if gen != nil {
		m.titleCh = make(chan titleResult)
		m.stop = make(chan struct{})
	}
	return m
}

func (m Model) Init() tea.Cmd {
	if m.gen == nil {
		return nil
	}
	var todo []model.Session
	for _, s := range m.sessions {
		if NeedsTitleGen(s) {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	go m.runGenerator(todo)
	return waitForTitle(m.titleCh)
}

// runGenerator generates titles for todo (newest-first) on a background
// goroutine, persisting each to the cache and streaming it to the Update loop.
// It exits promptly when stop is closed (e.g. the user picked a session).
func (m Model) runGenerator(todo []model.Session) {
	defer close(m.titleCh)

	// Tie the generation context to stop so closing it (Shutdown) aborts any
	// in-flight ollama request instead of blocking up to the client timeout.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-m.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	for _, s := range todo {
		select {
		case <-m.stop:
			return
		default:
		}
		title, err := m.gen.Generate(ctx, s.GenContext)
		if err != nil || title == "" {
			continue
		}
		m.cache.Set(s.ID, title, s.Size, s.FileModTime.UnixNano())
		_ = m.cache.Save() // cheap atomic write; keeps the cache crash-safe
		select {
		case m.titleCh <- titleResult{id: s.ID, title: title}:
		case <-m.stop:
			return
		}
	}
}

func waitForTitle(ch chan titleResult) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return titlesDoneMsg{}
		}
		return r
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil

	case titleResult:
		if idx, ok := m.idIndex[msg.id]; ok {
			m.sessions[idx].GenTitle = msg.title
			cmd := m.list.SetItem(idx, sessionItem{s: m.sessions[idx]})
			return m, tea.Batch(cmd, waitForTitle(m.titleCh))
		}
		return m, waitForTitle(m.titleCh)

	case titlesDoneMsg:
		return m, nil

	case tea.KeyMsg:
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(sessionItem); ok {
				s := it.s
				m.selected = &s
				return m, tea.Quit
			}
		case "c":
			m.syncChordAt = time.Time{}
			if !m.copyChordAt.IsZero() && time.Since(m.copyChordAt) <= chordTimeout {
				m.copyChordAt = time.Time{}
				return m, m.list.NewStatusMessage(m.copySelectedCommand())
			}
			m.copyChordAt = time.Now()
			return m, m.list.NewStatusMessage("もう一度 c でコマンドをクリップボードへコピー")
		case "g":
			m.copyChordAt = time.Time{}
			if !m.syncChordAt.IsZero() && time.Since(m.syncChordAt) <= chordTimeout {
				m.syncChordAt = time.Time{}
				startMsg, cmd := m.exportSelected()
				return m, tea.Batch(m.list.NewStatusMessage(startMsg), cmd)
			}
			m.syncChordAt = time.Now()
			return m, m.list.NewStatusMessage(m.previewSelectedExport())
		default:
			m.copyChordAt = time.Time{}
			m.syncChordAt = time.Time{}
		}

	case syncDoneMsg:
		switch {
		case isSyncNotConfigured(msg.err):
			return m, m.list.NewStatusMessage("sync未設定 — READMEの「Sync setup」セクション参照")
		case msg.err != nil:
			return m, m.list.NewStatusMessage(warnStyle.Render("[失敗]") + " 同期エラー: " + msg.err.Error())
		case msg.sha == "":
			return m, m.list.NewStatusMessage("同期: 変更なし（既にGitHubに最新版あり）")
		default:
			return m, m.list.NewStatusMessage("同期完了 → GitHub push成功: " + msg.sha[:8])
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	return japanize(m.list.View())
}

// Shutdown stops the background title generator and flushes the cache. Safe to
// call multiple times and when generation was never started.
func (m Model) Shutdown() {
	if m.stop != nil && m.stopOnce != nil {
		m.stopOnce.Do(func() { close(m.stop) })
	}
	if m.cache != nil {
		_ = m.cache.Save()
	}
}

// japanize patches English strings that bubbles/list hardcodes and doesn't
// expose via its public API.
var japanizeReplacer = strings.NewReplacer(
	"Nothing matched", "該当なし",
	"No セッション.", "セッションがありません。",
	"No セッション", "セッションなし",
	"filter applied", "絞り込み中",
	"unfiltered", "絞り込みなし",
	"filtering", "入力中",
)

func japanize(s string) string {
	return japanizeReplacer.Replace(s)
}

// Selected returns the session the user chose, or nil if they quit.
func (m Model) Selected() *model.Session { return m.selected }

// warnStyle highlights destructive/external-effect previews.
var warnStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("226")).
	Background(lipgloss.Color("88")).
	Bold(true).
	Padding(0, 1)

// previewSelectedExport / exportSelected / isSyncNotConfigured live in
// build-tag-gated files (sync_methods_sync.go / sync_methods_nosync.go) so
// the default `go build` produces a binary without any sync-package code.

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%dB", n)
	case n < k*k:
		return fmt.Sprintf("%.1fKB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1fMB", float64(n)/(k*k))
	default:
		return fmt.Sprintf("%.1fGB", float64(n)/(k*k*k))
	}
}

// copySelectedCommand builds and copies the "cd '<cwd>' && claude --resume
// <id>" line to the system clipboard, returning a status string describing
// the outcome.
func (m Model) copySelectedCommand() string {
	it, ok := m.list.SelectedItem().(sessionItem)
	if !ok {
		return "コピー対象が選択されていません"
	}
	cmd := fmt.Sprintf("cd %s && claude --resume %s", shellQuote(it.s.CWD), it.s.ID)
	if err := clipboard.WriteAll(cmd); err != nil {
		return fmt.Sprintf("コピー失敗: %v", err)
	}
	return "コピーしました: " + cmd
}

// shellQuote single-quotes a string for safe shell use, escaping any embedded
// single quotes with the standard '\” trick.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return filepath.Clean(p)
}

func runeLen(s string) int { return len([]rune(s)) }

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func humanizeDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "たった今"
	case d < time.Hour:
		return fmt.Sprintf("%d分前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d時間前", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d日前", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d週間前", int(d.Hours()/24/7))
	default:
		return fmt.Sprintf("%dヶ月前", int(d.Hours()/24/30))
	}
}
