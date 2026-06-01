package ui

import (
	"strings"
	"testing"

	"github.com/FallseF/Kioku/internal/scanner"
)

// TestHeadlineResolutionSmoke prints the resolved headline for the newest
// sessions so the cleaning/priority logic can be eyeballed against real data.
// It also fails if a headline still contains obvious harness noise.
func TestHeadlineResolutionSmoke(t *testing.T) {
	sessions, err := scanner.ScanAll()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(sessions) == 0 {
		t.Skip("no sessions on this machine; skipping")
	}

	noise := []string{"<command-", "<system-reminder", "Hello memory agent", "PROGRESS SUMMARY", "</", "<observed_from", "<user_request"}
	n := 40
	if len(sessions) < n {
		n = len(sessions)
	}
	for i := 0; i < n; i++ {
		it := sessionItem{s: sessions[i]}
		title := it.Title()
		t.Logf("%2d | %s", i+1, title)
		for _, bad := range noise {
			if strings.Contains(title, bad) {
				t.Errorf("session %s headline still has noise %q: %q", sessions[i].ID, bad, title)
			}
		}
	}
}
