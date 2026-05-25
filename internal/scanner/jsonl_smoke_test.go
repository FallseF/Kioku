package scanner

import (
	"testing"
)

func TestScanAllSmoke(t *testing.T) {
	sessions, err := ScanAll()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(sessions) == 0 {
		t.Skip("no sessions on this machine; skipping")
	}
	first := sessions[0]
	t.Logf("found %d sessions", len(sessions))
	t.Logf("newest: id=%s cwd=%s title=%q msgs=%d updated=%s",
		first.ID, first.CWD, first.Title, first.MessageCount, first.LastUpdated)
	if first.ID == "" {
		t.Error("first session has empty ID")
	}
	if first.LastUpdated.IsZero() {
		t.Error("first session has zero LastUpdated")
	}
}
