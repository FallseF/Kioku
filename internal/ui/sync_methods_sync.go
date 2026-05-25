//go:build sync

package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/FallseF/Kioku/internal/sync"
)

func (m Model) previewSelectedExport() string {
	it, ok := m.list.SelectedItem().(sessionItem)
	if !ok {
		return "対象が選択されていません"
	}
	if _, err := os.Stat(sync.RepoPath()); err != nil {
		return "sync未設定: " + sync.RepoPath() + " に repo を clone してください (READMEのSync setup参照)"
	}
	report, err := sync.Preview(&it.s)
	if err != nil {
		return "プレビュー失敗: " + err.Error()
	}

	includes := []string{".jsonl"}
	if report.MemoryDir != "" {
		includes = append(includes, "memory/")
	}
	if report.CLAUDEMDPath != "" {
		includes = append(includes, "CLAUDE.md")
	}

	return fmt.Sprintf("%s GitHubへpush(%s, %s) — もう一度 g で実行",
		warnStyle.Render("[警告]"),
		strings.Join(includes, "+"),
		humanBytes(report.TotalBytes),
	)
}

func (m Model) exportSelected() (string, tea.Cmd) {
	it, ok := m.list.SelectedItem().(sessionItem)
	if !ok {
		return "対象が選択されていません", nil
	}
	session := it.s
	startMsg := warnStyle.Render("[push中]") + " GitHubへ送信しています…"
	cmd := func() tea.Msg {
		sha, err := sync.Export(&session)
		return syncDoneMsg{sha: sha, err: err}
	}
	return startMsg, cmd
}

func isSyncNotConfigured(err error) bool {
	return errors.Is(err, sync.ErrNotConfigured)
}

// syncEnabledMarker is referenced by the sync build's main wiring so callers
// can distinguish the two binaries at runtime.
const syncEnabledMarker = true
