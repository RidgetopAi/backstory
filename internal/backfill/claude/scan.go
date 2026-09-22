package claude

import (
	"bufio"
	"io"
	"os"
)

// rawLine is one line read from a transcript file: Text is the line's raw
// bytes with any trailing newline stripped; End is the byte offset
// immediately after the line (including its newline, when present) — the
// value a cursor resumes from on the next run.
type rawLine struct {
	Text []byte
	End  int64
}

// readLinesFrom reads path's lines starting at byte offset from, with no
// line-length cap. bufio.Scanner's default 64KiB token would truncate or
// error on a long transcript line (PLAN.md §Phase 3: measured 65KB in the
// wild, and a fixture here reaches 1MiB), so this reads with bufio.Reader
// instead, whose buffer grows to fit whatever ReadBytes needs.
func readLinesFrom(path string, from int64) ([]rawLine, error) {
	f, err := os.Open(path) //nolint:gosec // path is produced by filepath.Glob over the configured backfill root, never caller input
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	if from > 0 {
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return nil, err
		}
	}

	r := bufio.NewReader(f)
	var lines []rawLine
	offset := from
	for {
		chunk, readErr := r.ReadBytes('\n')
		// A chunk with no trailing newline is a partial tail: the harness
		// may still be mid-write on it. It must not be treated as a line
		// and the cursor must not advance past its bytes, or a rerun would
		// silently lose whatever the writer appends to complete it.
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			offset += int64(len(chunk))
			lines = append(lines, rawLine{Text: chunk[:len(chunk)-1], End: offset})
		}
		if readErr != nil {
			if readErr == io.EOF { //nolint:errorlint // bufio.Reader.ReadBytes returns io.EOF verbatim, never wrapped
				break
			}
			return nil, readErr
		}
	}
	return lines, nil
}
