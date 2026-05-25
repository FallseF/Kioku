package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/FallseF/Kioku/internal/launcher"
	"github.com/FallseF/Kioku/internal/scanner"
	"github.com/FallseF/Kioku/internal/ui"
)

func main() {
	includeBackground := flag.Bool("all", false, "claude-memのobserver-sessionsなどバックグラウンドセッションも含める")
	flag.Parse()

	sessions, err := scanner.ScanAllWithOptions(scanner.Options{
		IncludeBackground: *includeBackground,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "セッション読み込み失敗: %v\n", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "~/.claude/projects/ にセッションが見つかりません")
		os.Exit(0)
	}

	p := tea.NewProgram(ui.NewModel(sessions), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "UI起動失敗: %v\n", err)
		os.Exit(1)
	}

	m, ok := finalModel.(ui.Model)
	if !ok || m.Selected() == nil {
		return
	}
	s := m.Selected()

	if err := launcher.Resume(s.CWD, s.ID); err != nil {
		fmt.Fprintf(os.Stderr, "セッション再開失敗: %v\n", err)
		os.Exit(1)
	}
}
