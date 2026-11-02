package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

func seedBackupStore(t *testing.T, dbPath string) {
	t.Helper()
	seedDeletableRecord(t, dbPath, "proj-one", "first")
	seedDeletableRecord(t, dbPath, "proj-two", "second")
}

func backupEnv(t *testing.T) (data string, dbPath string) {
	t.Helper()
	data = t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	dbPath = filepath.Join(data, "backstory", "backstory.db")
	seedBackupStore(t, dbPath)
	return data, dbPath
}

func TestBackupDefaultPathModesAndJSON(t *testing.T) {
	data, _ := backupEnv(t)
	var out, errb bytes.Buffer
	if code := run([]string{"backup", "--json"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var info store.BackupInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v: %s", err, out.String())
	}
	dir := filepath.Join(data, "backstory", "backups")
	if filepath.Dir(info.Path) != dir || !regexp.MustCompile(`^backstory-\d{8}T\d{6}Z\.db$`).MatchString(filepath.Base(info.Path)) {
		t.Fatalf("path = %s", info.Path)
	}
	di, _ := os.Stat(dir)
	fi, err := os.Stat(info.Path)
	if err != nil || di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("modes dir=%v file=%v err=%v", di.Mode().Perm(), fi.Mode().Perm(), err)
	}
	if info.Size != fi.Size() || info.Projects != 2 || info.Records != 2 || info.SchemaVersion < 1 {
		t.Fatalf("info = %+v (file size %d)", info, fi.Size())
	}
}

func TestBackupOutTextAndForce(t *testing.T) {
	_, dbPath := backupEnv(t)
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := st.Version()
	_ = st.Close()

	dest := filepath.Join(t.TempDir(), "x", "mine.db")
	var out, errb bytes.Buffer
	if code := run([]string{"backup", "--out", dest}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, line := range []string{"path: " + dest, "projects: 2", "sessions: 0", "records: 2", "schema version: " + strconv.Itoa(want)} {
		if !strings.Contains(out.String(), line+"\n") {
			t.Errorf("output lacks %q:\n%s", line, out.String())
		}
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"backup", "--out", dest}, strings.NewReader(""), &out, &errb); code == 0 || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("second backup: exit %d, stderr %q; want refusal naming --force", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"backup", "--out", dest, "--force"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("--force exit %d: %s", code, errb.String())
	}
}

func TestBackupWithoutStoreFails(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"backup"}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatalf("exit 0 with no store; stdout %q", out.String())
	}
}
