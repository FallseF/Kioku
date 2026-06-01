package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/FallseF/Kioku/internal/launcher"
	"github.com/FallseF/Kioku/internal/model"
	"github.com/FallseF/Kioku/internal/scanner"
	"github.com/FallseF/Kioku/internal/titler"
	"github.com/FallseF/Kioku/internal/ui"
)

func main() {
	includeBackground := flag.Bool("all", false, "claude-memのobserver-sessionsやagent-*など、バックグラウンド/サブエージェントセッションも含める")
	warmTitles := flag.Bool("warm-titles", false, "ollamaで見出しの無いセッションの日本語タイトルを一括生成し、キャッシュに焼く（TUIは開かない）")
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

	cache := titler.LoadCache()
	for i := range sessions {
		if t, ok := cache.Get(sessions[i].ID, sessions[i].Size, sessions[i].FileModTime.UnixNano()); ok {
			sessions[i].GenTitle = t
		}
	}

	gen := titler.New()
	available := gen.Available(context.Background())

	if *warmTitles {
		if !available {
			fmt.Fprintf(os.Stderr, "ollama に接続できません (%s)。起動状態と KIOKU_OLLAMA_URL を確認してください。\n", gen.Endpoint)
			os.Exit(1)
		}
		warmTitlesRun(gen, cache, sessions)
		return
	}

	// Local title generation is on by default when ollama is reachable; set
	// KIOKU_TITLES=off to opt out of the localhost calls entirely.
	var activeGen *titler.Generator
	if available && os.Getenv("KIOKU_TITLES") != "off" {
		activeGen = gen
	}

	p := tea.NewProgram(ui.NewModel(sessions, activeGen, cache), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "UI起動失敗: %v\n", err)
		os.Exit(1)
	}

	m, ok := finalModel.(ui.Model)
	if !ok {
		return
	}
	m.Shutdown() // stop background title generation and flush the cache

	if m.Selected() == nil {
		return
	}
	s := m.Selected()
	if err := launcher.Resume(s.CWD, s.ID); err != nil {
		fmt.Fprintf(os.Stderr, "セッション再開失敗: %v\n", err)
		os.Exit(1)
	}
}

// warmTitlesRun generates titles for every session that lacks a good headline
// and isn't already cached, printing progress to stderr and persisting the
// cache periodically and at the end.
func warmTitlesRun(gen *titler.Generator, cache *titler.Cache, sessions []model.Session) {
	var todo []model.Session
	for _, s := range sessions {
		if ui.NeedsTitleGen(s) {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(os.Stderr, "生成が必要なセッションはありません（すべてキャッシュ済み、または既に見出しあり）")
		return
	}

	fmt.Fprintf(os.Stderr, "%d 件のタイトルを生成します（モデル: %s）...\n", len(todo), gen.Model)
	ctx := context.Background()
	done, failed := 0, 0
	var lastErr error
	for i, s := range todo {
		title, err := gen.Generate(ctx, s.GenContext)
		if err != nil {
			failed++
			lastErr = err
			continue
		}
		if title == "" {
			continue
		}
		cache.Set(s.ID, title, s.Size, s.FileModTime.UnixNano())
		done++
		fmt.Fprintf(os.Stderr, "\r[%d/%d] %s\033[K", i+1, len(todo), title)
		_ = cache.Save() // save after each so a Ctrl-C never wastes a finished call
	}
	if err := cache.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "\nキャッシュ保存失敗: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\n完了: %d/%d 件生成 → %s\n", done, len(todo), titler.CachePath())
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d 件は生成に失敗しました（最後のエラー: %v）\n", failed, lastErr)
	}
}
