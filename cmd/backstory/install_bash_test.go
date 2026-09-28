package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/install"
	"github.com/RidgetopAi/backstory/internal/project"
)

// installBashSessionEnv builds the env `backstory install bash` and the
// bash session it wires up both run under: daemonEnv (startTestDaemon's own
// return) with HOME replaced by home and binDir prepended to PATH — the
// same shape shell_test.go's shellSessionEnv builds, except HOME is the
// caller's own choice rather than a fresh t.TempDir() picked internally,
// since this test needs to install into, and then start a real shell
// against, the very same HOME.
func installBashSessionEnv(daemonEnv []string, home, binDir string) []string {
	out := make([]string, 0, len(daemonEnv)+2)
	sawPath := false
	for _, e := range daemonEnv {
		switch {
		case strings.HasPrefix(e, "HOME="):
			continue
		case strings.HasPrefix(e, "PATH="):
			out = append(out, "PATH="+binDir+":"+strings.TrimPrefix(e, "PATH="))
			sawPath = true
		default:
			out = append(out, e)
		}
	}
	if !sawPath {
		out = append(out, "PATH="+binDir+":"+os.Getenv("PATH"))
	}
	out = append(out, "HOME="+home)
	return out
}

// TestInstallBashEndToEndRealBashRecordsTrueCommand is the punch's DONE
// WHEN clause 3: after `backstory install bash` in a temp HOME, a real
// `bash -i` started there (with no --norc, so it sources ~/.bashrc exactly
// the way a real login session would) records a `true` command as a shell
// event in a test daemon — end-to-end with part 1 (task 7fe84ffb)'s
// preexec/precmd capture.
func TestInstallBashEndToEndRealBashRecordsTrueCommand(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	projectDir := t.TempDir()
	dbPath, _, daemonEnv := startTestDaemon(t, bin, shellAncestry(projectDir)...)
	env := installBashSessionEnv(daemonEnv, home, filepath.Dir(bin))

	installCmd := exec.Command(bin, "install", "bash") //nolint:gosec // bin is the binary this test just built
	installCmd.Env = env
	if out, err := installCmd.CombinedOutput(); err != nil {
		t.Fatalf("backstory install bash: %v\n%s", err, out)
	}

	bashrcPath := filepath.Join(home, ".bashrc")
	bashrcData, err := os.ReadFile(bashrcPath) //nolint:gosec // bashrcPath is this test's own t.TempDir() path, not external input
	if err != nil {
		t.Fatalf("read ~/.bashrc after install: %v", err)
	}
	if !strings.Contains(string(bashrcData), install.BashrcMarkerComment) {
		t.Fatalf("~/.bashrc after `backstory install bash` missing marker comment: %q", bashrcData)
	}

	// A real, non-login interactive bash sources ~/.bashrc on its own — no
	// eval line needed in this script, unlike shell_test.go's tests, which
	// eval the snippet directly and deliberately never touch a real
	// ~/.bashrc at all.
	cmd := exec.Command("bash", "-i") //nolint:gosec // fixed args, this test's own script feeds stdin
	cmd.Dir = projectDir
	cmd.Env = env
	cmd.Stdin = strings.NewReader("true\nexit 0\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("run bash -i sourcing installed ~/.bashrc: %v\noutput:\n%s", err, out.String())
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := waitForShellCommandEventCount(t, s, projectKey, 1, 3*time.Second)

	found := false
	for _, ev := range events {
		if ev.Cmd == "true" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no shell command event for \"true\" among %#v", events)
	}
}
