package launcher

import (
	"fmt"
	"os"
	gexec "os/exec"
	"regexp"
	"syscall"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// Resume replaces the current process with `claude --resume <sessionID>` in cwd.
// The TUI must have exited before calling this. Uses the exec(2) syscall (no
// shell), so injection is impossible; sessionID is validated as a UUID.
func Resume(cwd, sessionID string) error {
	if !uuidRegex.MatchString(sessionID) {
		return fmt.Errorf("invalid session id: %q", sessionID)
	}
	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("chdir %q: %w", cwd, err)
	}
	bin, err := gexec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}
	return syscall.Exec(bin, []string{"claude", "--resume", sessionID}, os.Environ())
}
