package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FallseF/Kioku/internal/model"
)

func TestPreview(t *testing.T) {
	home, _ := os.UserHomeDir()
	projectsRoot := filepath.Join(home, ".claude", "projects")

	dirs, err := os.ReadDir(projectsRoot)
	if err != nil {
		t.Skipf("no claude projects dir: %v", err)
	}
	var jsonlPath string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projectsRoot, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) == ".jsonl" {
				jsonlPath = filepath.Join(projectsRoot, d.Name(), f.Name())
				break
			}
		}
		if jsonlPath != "" {
			break
		}
	}
	if jsonlPath == "" {
		t.Skip("no jsonl found")
	}
	s := &model.Session{
		ID:           "smoke-test",
		FilePath:     jsonlPath,
		CWD:          home,
		MessageCount: 1,
	}
	report, err := Preview(s)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	t.Logf("target=%s files=%d bytes=%d session=%s memory=%s claudeMd=%s",
		report.TargetDir, report.FileCount, report.TotalBytes,
		report.SessionPath, report.MemoryDir, report.CLAUDEMDPath)
	if report.FileCount == 0 {
		t.Error("preview returned 0 files")
	}
}
