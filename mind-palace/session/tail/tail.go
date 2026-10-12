// Package tail polls files and directories for changes: a line tailer, a
// directory change watcher, and the session artifact watchers built on them.
// Polling only: fsnotify is unreliable for new files on macOS and the poll
// loop is the portable safety net the Node implementation also relied on.
package tail

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultInterval     = 2 * time.Second
	defaultWaitInterval = 500 * time.Millisecond
)

// Options tunes the poll loops. Zero values select the defaults.
type Options struct {
	Interval     time.Duration // poll period once the target exists (default 2 s)
	WaitInterval time.Duration // poll period until the target exists (default 500 ms)
	Logger       *slog.Logger  // default slog.Default()
}

func (o Options) withDefaults() Options {
	if o.Interval <= 0 {
		o.Interval = defaultInterval
	}
	if o.WaitInterval <= 0 {
		o.WaitInterval = defaultWaitInterval
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

// poll runs step on its own goroutine until stopped. step reports whether its
// target exists, which selects the wait or steady interval for the next round.
// The returned stop is idempotent and returns after the goroutine has exited.
func poll(opts Options, step func() (exists bool)) (stop func()) {
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			delay := opts.WaitInterval
			if step() {
				delay = opts.Interval
			}
			select {
			case <-quit:
				return
			case <-time.After(delay):
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}

// TailFile delivers each complete non-blank line of the file to onLine, in
// order, polling from the last byte offset. It waits for the file to exist.
// A trailing line without a newline is held until it is completed.
func TailFile(path string, onLine func(string), opts Options) (stop func()) {
	opts = opts.withDefaults()
	var pos int64
	return poll(opts, func() bool {
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		if info.Size() > pos {
			pos += readLines(path, pos, onLine, opts.Logger)
		}
		return true
	})
}

// readLines delivers the complete lines found at offset and returns the number
// of bytes consumed. Transient read errors consume nothing so the next poll retries.
func readLines(path string, offset int64, onLine func(string), log *slog.Logger) int64 {
	lines, end, err := ReadLines(path, offset)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn("tail read failed", "path", path, "err", err)
		}
		return 0
	}
	for _, l := range lines {
		onLine(l.Text)
	}
	return end - offset
}

// Line is one complete non-blank line of a file and the byte offset just past
// its newline, which is where a reader resumes after storing it.
type Line struct {
	Text string
	End  int64
}

// ReadLines returns the complete non-blank lines found from offset to the end
// of the file, in order, and the offset just past the last complete line (a
// trailing line without a newline is left for the next read). Blank lines are
// consumed but not returned and a trailing "\r" is stripped, exactly as
// TailFile delivers lines; numbering the returned lines consecutively from
// the count stored so far reproduces TailFile's seq scheme.
func ReadLines(path string, offset int64) (lines []Line, end int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}
	complete := bytes.LastIndexByte(data, '\n') + 1
	pos := 0
	for pos < complete {
		nl := bytes.IndexByte(data[pos:complete], '\n')
		raw := strings.TrimSuffix(string(data[pos:pos+nl]), "\r")
		pos += nl + 1
		if strings.TrimSpace(raw) != "" {
			lines = append(lines, Line{Text: raw, End: offset + int64(pos)})
		}
	}
	return lines, offset + int64(complete), nil
}

// DecodeLine parses a transcript line as JSON; a line that is not JSON is
// kept verbatim as {"raw": line} so nothing captured is lost.
func DecodeLine(raw string) any {
	var line any
	if err := json.Unmarshal([]byte(raw), &line); err != nil {
		return map[string]any{"raw": raw}
	}
	return line
}
