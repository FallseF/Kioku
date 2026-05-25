//go:build !sync

package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

const syncDisabledMessage = "gg (別PC同期) はこのビルドに含まれていません — go build -tags sync で再ビルドしてください"

func (m Model) previewSelectedExport() string {
	return syncDisabledMessage
}

func (m Model) exportSelected() (string, tea.Cmd) {
	return syncDisabledMessage, nil
}

func isSyncNotConfigured(err error) bool {
	return false
}

const syncEnabledMarker = false
