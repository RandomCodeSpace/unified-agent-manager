package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/daemonruntime"
)

// cliOwnerEnv marks each Copilot CLI uam starts with the uam process that
// owns it, as "<pid>:<start time>", so a later uam can tell a CLI left
// behind by a uam that died from one another uam still runs.
const cliOwnerEnv = "UAM_CLI_OWNER"

// cliOwner is this process's mark, or "" where process start times cannot
// be read.
func cliOwner() string {
	pid := os.Getpid()
	start := daemonruntime.ProcStartTime(pid)
	if start == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", pid, start)
}

// procDir is where sweepOrphanCLIs reads processes.
const procDir = "/proc"

// sweepOrphanCLIs kills the Copilot CLIs a uam that is gone left running:
// a CLI that hung when its uam died never sees its input close. It kills
// only this user's processes started with the SDK's --headless and --stdio
// arguments whose cliOwnerEnv names a uam no longer running, and reports
// how many. Another live uam's CLIs are left alone, as is any process
// without the mark.
func sweepOrphanCLIs() int {
	dirs, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*"))
	killed := 0
	for _, dir := range dirs {
		pid, err := strconv.Atoi(filepath.Base(dir))
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")) // #nosec G304 -- a /proc entry.
		if err != nil {
			continue
		}
		argv := strings.Split(string(cmdline), "\x00")
		if !slices.Contains(argv, "--headless") || !slices.Contains(argv, "--stdio") {
			continue
		}
		start := daemonruntime.ProcStartTime(pid)
		// Only the mark is read; another user's environment is unreadable.
		environ, err := os.ReadFile(filepath.Join(dir, "environ")) // #nosec G304 -- a /proc entry.
		if err != nil {
			continue
		}
		owner := ""
		for kv := range strings.SplitSeq(string(environ), "\x00") {
			if v, ok := strings.CutPrefix(kv, cliOwnerEnv+"="); ok {
				owner = v
			}
		}
		if owner == "" || ownerRunning(owner) || start == 0 || daemonruntime.ProcStartTime(pid) != start {
			continue
		}
		if syscall.Kill(pid, syscall.SIGKILL) == nil {
			killed++
		}
	}
	return killed
}

// ownerRunning reports whether the uam a cliOwnerEnv mark names still runs.
// A mark it cannot read counts as running, so its CLI is left alone.
func ownerRunning(owner string) bool {
	pidText, startText, ok := strings.Cut(owner, ":")
	pid, err := strconv.Atoi(pidText)
	start, serr := strconv.ParseInt(startText, 10, 64)
	if !ok || err != nil || serr != nil || pid <= 0 || start == 0 {
		return true
	}
	return daemonruntime.ProcAlive(pid) && daemonruntime.ProcStartTime(pid) == start
}
