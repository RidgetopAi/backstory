package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// runBackstoryEnv runs bin with args against env, returning stdout, stderr, exit code.
func runBackstoryEnv(t *testing.T, bin string, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	var outBuf, errBuf safeBuffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v (stderr: %s)", args, err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestBareInstallSkipsHermesForeignProviderAndExitsZero is the CLI end of the
// install package's conflict test: claude, codex, pi installed, hermes named as
// skipped, exit 0, every hermes byte as it was.
func TestBareInstallSkipsHermesForeignProviderAndExitsZero(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".pi/agent", ".hermes"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(home, ".hermes", "config.yaml")
	const yaml = "memory:\n  provider: holographic\n"
	if err := os.WriteFile(cfg, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	stdout, stderr, code := runBackstoryEnv(t, bin, env, "install", "--no-verify")
	if code != 0 {
		t.Fatalf("bare install exit = %d, want 0 (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	for _, w := range []string{"claude: installed", "codex: installed", "pi: installed"} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
	if !strings.Contains(stdout, "hermes: skipped") || !strings.Contains(stdout, "memory.provider") {
		t.Errorf("stdout lacks a hermes skipped line naming the provider conflict:\n%s", stdout)
	}
	if b, _ := os.ReadFile(cfg); string(b) != yaml { //nolint:gosec // fixture path under t.TempDir
		t.Errorf("hermes config.yaml changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes", "plugins")); err == nil {
		t.Errorf("hermes plugin directory was created despite the conflict")
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes", "skills")); err == nil {
		t.Errorf("hermes skills directory was created despite the conflict")
	}
}

// TestInstallCheckDialsTheDaemon: --check reports the daemon as not answering
// when nothing listens on the socket, and as answering when a daemon does.
func TestInstallCheckDialsTheDaemon(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()

	idle := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "XDG_RUNTIME_DIR=" + t.TempDir()}
	stdout, _, _ := runBackstoryEnv(t, bin, idle, "install", "claude", "--check")
	if !strings.Contains(stdout, "daemon: not answering") {
		t.Errorf("no listener: stdout = %q, want daemon: not answering", stdout)
	}

	_, _, daemonEnv := startTestDaemon(t, bin)
	live := mergeEnv(daemonEnv, "HOME="+home)
	stdout, _, _ = runBackstoryEnv(t, bin, live, "install", "claude", "--check")
	if !strings.Contains(stdout, "daemon: answering") {
		t.Errorf("test daemon up: stdout = %q, want daemon: answering", stdout)
	}
}

// TestInstallBashPrintsOneLine: a successful `install bash` says what it
// changed, on one non-empty line.
func TestInstallBashPrintsOneLine(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	stdout, stderr, code := runBackstoryEnv(t, bin, env, "install", "bash")
	if code != 0 {
		t.Fatalf("install bash exit = %d (stderr: %s)", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		t.Errorf("install bash stdout = %q, want exactly one non-empty line", stdout)
	}
}

// TestInstallSelfCheckLeavesNoSessionRow: the Claude installer's hook
// verification against a live daemon must not mint a session. The invoking
// environment's XDG_RUNTIME_DIR is stripped and a fresh one supplied, so the
// result is the same whether or not the runner has one set.
func TestInstallSelfCheckLeavesNoSessionRow(t *testing.T) {
	for _, inheritXDG := range []bool{true, false} {
		name := "invoking env without XDG_RUNTIME_DIR"
		if inheritXDG {
			name = "invoking env with XDG_RUNTIME_DIR"
		}
		t.Run(name, func(t *testing.T) {
			if inheritXDG {
				t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // a decoy the subprocesses must not use
			} else {
				t.Setenv("XDG_RUNTIME_DIR", "")
			}
			bin := buildBackstory(t)
			home := t.TempDir()
			// An empty HOME for the daemon too: its startup backfill would
			// otherwise import the runner's real Claude sessions mid-test.
			t.Setenv("HOME", home)
			dbPath, _, daemonEnv := startTestDaemon(t, bin)
			env := mergeEnv(daemonEnv, "HOME="+home)

			sessions := func() int {
				t.Helper()
				st, err := store.Open(dbPath, nil, project.RealGit{})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = st.Close() }()
				var n int
				if err := st.DB().QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			before := sessions()

			stdout, stderr, code := runBackstoryEnv(t, bin, env, "install", "claude") // verification ON
			if code != 0 {
				t.Fatalf("install claude exit = %d (stdout: %s, stderr: %s)", code, stdout, stderr)
			}
			if after := sessions(); after != before {
				t.Errorf("sessions rows = %d after the installer's self-check, want %d (unchanged)", after, before)
			}
		})
	}
}
