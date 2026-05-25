package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/FallseF/Kioku/internal/model"
)

const chordTimeout = 1500 * time.Millisecond

type sessionItem struct {
	s model.Session
}

func (i sessionItem) FilterValue() string {
	return i.s.Title + " " + i.s.FirstMessage + " " + i.s.CWD
}

func (i sessionItem) Title() string {
	if i.s.FirstMessage != "" {
		return i.s.FirstMessage
	}
	if i.s.Title != "" {
		return i.s.Title
	}
	return "(無題)"
}

func (i sessionItem) Description() string {
	cwd := shortenPath(i.s.CWD)
	age := humanizeDuration(time.Since(i.s.LastUpdated))
	return fmt.Sprintf("%s  ·  %s  ·  %d件", cwd, age, i.s.MessageCount)
}

type Model struct {
	list        list.Model
	selected    *model.Session
	width       int
	height      int
	copyChordAt time.Time // when the first 'c' of a "cc" chord was pressed; zero = not pending
	syncChordAt time.Time // when the first 'g' of a "gg" chord was pressed; zero = not pending
}

// syncDoneMsg is the result of an asynchronous sync.Export call.
type syncDoneMsg struct {
	sha string
	err error
}

func NewModel(sessions []model.Session) Model {
	items := make([]list.Item, len(sessions))
	for i, s := range sessions {
		items[i] = sessionItem{s: s}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("212")).
		BorderLeftForeground(lipgloss.Color("212"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("212")).
		BorderLeftForeground(lipgloss.Color("212"))

	l := list.New(items, delegate, 0, 0)
	l.Title = "ClaudeHistory"
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

	return Model{list: l}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, msg.Height)
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
// single quotes with the standard '\'' trick.
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
