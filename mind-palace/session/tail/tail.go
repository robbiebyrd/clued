// Package tail polls files and directories for changes: a line tailer, a
// directory change watcher, and the session artifact watchers built on them.
// Polling only: fsnotify is unreliable for new files on macOS and the poll
// loop is the portable safety net the Node implementation also relied on.
package tail

import (
	"bytes"
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
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0
	}
	data, err := io.ReadAll(f)
	if err != nil {
		log.Warn("tail read failed", "path", path, "err", err)
		return 0
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	for _, line := range strings.Split(string(data[:end]), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) != "" {
			onLine(line)
		}
	}
	return int64(end)
}
