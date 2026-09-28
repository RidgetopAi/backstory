package codex

import (
	"bufio"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rawLine is one line read from a rollout file: Text is the line's raw
// bytes with any trailing newline stripped; End is the byte offset
// immediately after the line (including its newline) — the value a cursor
// resumes from on the next run.
type rawLine struct {
	Text []byte
	End  int64
}

// readLinesFrom reads path's lines starting at byte offset from, with no
// line-length cap (mirrors internal/backfill/claude/scan.go: bufio.Scanner's
// default 64KiB token would truncate or error on a long transcript line).
// partial reports whether the read ended on a trailing chunk with no
// newline — a rollout the harness may still be mid-write on. Such a chunk
// is excluded from lines and never advances the cursor, so a rerun picks it
// up complete once the writer finishes (DONE WHEN clause 3): the file's
// other, complete lines still import and the run itself never fails.
func readLinesFrom(path string, from int64) (lines []rawLine, partial bool, err error) {
	f, err := os.Open(path) //nolint:gosec // path comes from this package's own root walk (walkRollouts), never caller input
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	if from > 0 {
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return nil, false, err
		}
	}

	r := bufio.NewReader(f)
	offset := from
	for {
		chunk, readErr := r.ReadBytes('\n')
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			offset += int64(len(chunk))
			lines = append(lines, rawLine{Text: chunk[:len(chunk)-1], End: offset})
		} else if len(chunk) > 0 {
			partial = true
		}
		if readErr != nil {
			if readErr == io.EOF { //nolint:errorlint // bufio.Reader.ReadBytes returns io.EOF verbatim, never wrapped
				break
			}
			return nil, false, readErr
		}
	}
	return lines, partial, nil
}

// walkRollouts finds every rollout-*.jsonl file under root at any depth
// (~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl), sorted so a run
// processes them in a stable order. A root that does not exist is zero
// files, never an error (the same rule internal/backfill/claude's
// filepath.Glob gets for free): a fresh install with no ~/.codex yet must
// not fail the daemon's on-start run.
func walkRollouts(root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// peekSessionMetaBufSize bounds how long a single line peekSessionMeta will
// buffer while scanning for a session_meta line. Generous relative to a
// session_meta line's real size (a few hundred bytes) so it never rejects a
// legitimate file; a line beyond it degrades to "no session_meta found"
// (classified as a top-level session) rather than failing the run.
const peekSessionMetaBufSize = 10 * 1024 * 1024

// peekSessionMeta scans path from its start for the first session_meta
// line, independently of any saved cursor and without advancing one. It
// lets Import classify a file (top-level session vs. subagent) before
// running its normal, cursor-relative import (task e9cb97dd: every
// top-level session must exist before a subagent rollout looks up its
// parent, regardless of filename order). ok is false when the file has no
// session_meta line at all — malformed, truncated before its first
// complete line, or a line too long to buffer — in which case the caller
// treats it as a top-level session, never an error.
func peekSessionMeta(path string) (sessionMetaPayload, bool, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from this package's own root walk (walkRollouts), never caller input
	if err != nil {
		return sessionMetaPayload{}, false, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), peekSessionMetaBufSize)
	for sc.Scan() {
		line, perr := parseLine(sc.Bytes())
		if perr != nil {
			continue
		}
		if line.Type != "session_meta" {
			continue
		}
		meta, merr := parseSessionMeta(line.Payload)
		if merr != nil {
			continue
		}
		return meta, true, nil
	}
	return sessionMetaPayload{}, false, nil
}
