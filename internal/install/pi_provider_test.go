package install

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type piParitySummary struct {
	Tools      []string `json:"tools"`
	Injections int      `json:"injections"`
	Hooks      []struct {
		Argv  []string       `json:"argv"`
		Stdin map[string]any `json:"stdin"`
	} `json:"hooks"`
}

// runPiProviderSession drives one Pi session whose model provider is
// provider/api through the emitted extension and returns its summary. The
// environment is built from scratch (HOME, XDG_*, PATH are set here, nothing is
// inherited), so the result cannot depend on the invoking shell.
func runPiProviderSession(t *testing.T, provider, api string) piParitySummary {
	t.Helper()
	node := requireNode(t)
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	for _, d := range []string{home, filepath.Join(home, ".config"), filepath.Join(home, ".local", "share"), filepath.Join(home, ".local", "state"), filepath.Join(home, ".cache")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), PiExtensionSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeSrc, err := os.ReadFile(filepath.Join("testdata", "fake_backstory.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "backstory")
	if err := os.WriteFile(fake, fakeSrc, 0o700); err != nil { //nolint:gosec // test fake must be executable
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "fake.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner, _ := filepath.Abs(filepath.Join("testdata", "pi_provider_parity.mjs"))
	cmd := exec.Command(node, runner, filepath.Join(dir, "index.ts"), provider, api) //nolint:gosec // test-controlled args
	cmd.Env = []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"PATH=" + filepath.Dir(node) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
		"BACKSTORY_BIN=" + fake,
		"FAKE_LOG=" + logPath,
		"FAKE_HOOK_FAIL=",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("provider %s session failed: %v\n%s", provider, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "SUMMARY "); ok {
			var s piParitySummary
			if err := json.Unmarshal([]byte(rest), &s); err != nil {
				t.Fatal(err)
			}
			return s
		}
	}
	t.Fatalf("provider %s: no SUMMARY line in output:\n%s", provider, out)
	return piParitySummary{}
}

// TestPiLiveCaptureProviderParity: the Pi extension's live path (MCP tool
// registration, the single warm-block injection, post-tool-use hooks) is the
// same under a local OpenAI-compatible provider as under a hosted one.
func TestPiLiveCaptureProviderParity(t *testing.T) {
	local := runPiProviderSession(t, "local-qwen", "openai-completions")
	hosted := runPiProviderSession(t, "anthropic", "anthropic-messages")

	want := []string{"confirm", "note", "recall", "status", "timeline"}
	for name, s := range map[string]piParitySummary{"local-qwen": local, "anthropic": hosted} {
		if !reflect.DeepEqual(s.Tools, want) {
			t.Errorf("%s: registered tools = %v, want %v", name, s.Tools, want)
		}
		if s.Injections != 1 {
			t.Errorf("%s: warm-block injections = %d, want exactly 1", name, s.Injections)
		}
		if len(s.Hooks) != 2 {
			t.Errorf("%s: post-tool-use invocations = %d, want 2", name, len(s.Hooks))
		}
		for _, h := range s.Hooks {
			if !reflect.DeepEqual(h.Argv, []string{"hook", "post-tool-use", "--harness", "pi"}) {
				t.Errorf("%s: hook argv = %v", name, h.Argv)
			}
		}
	}
	if !reflect.DeepEqual(local, hosted) {
		t.Errorf("local and hosted sessions diverge:\nlocal  = %+v\nhosted = %+v", local, hosted)
	}
}
