package tail

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

var fast = Options{Interval: 10 * time.Millisecond, WaitInterval: 10 * time.Millisecond}

const deadline = 2 * time.Second

// collector gathers delivered lines safely across the tailer goroutine.
type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) add(l string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, l)
}

func (c *collector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func equal(c *collector, want ...string) func() bool {
	return func() bool { return reflect.DeepEqual(c.snapshot(), want) }
}

func TestTailFileDeliversLinesPresentAtStart(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, f, "{\"a\":1}\n\n   \n{\"b\":2}\n")
	c := &collector{}
	stop := TailFile(f, c.add, fast)
	defer stop()
	eventually(t, "initial lines", equal(c, `{"a":1}`, `{"b":2}`))
}

func TestTailFileDeliversLinesAppendedAfterStart(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, f, "{\"a\":1}\n")
	c := &collector{}
	stop := TailFile(f, c.add, fast)
	defer stop()
	eventually(t, "first line", equal(c, `{"a":1}`))
	appendTo(t, f, "{\"b\":2}\n")
	eventually(t, "appended line", equal(c, `{"a":1}`, `{"b":2}`))
}

func TestTailFileWaitsForFileToAppear(t *testing.T) {
	f := filepath.Join(t.TempDir(), "late.jsonl")
	c := &collector{}
	stop := TailFile(f, c.add, fast)
	defer stop()
	time.Sleep(50 * time.Millisecond)
	write(t, f, "{\"x\":1}\n")
	eventually(t, "line from late file", equal(c, `{"x":1}`))
}

func TestTailFileStopHaltsDelivery(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, f, "{\"a\":1}\n")
	c := &collector{}
	stop := TailFile(f, c.add, fast)
	eventually(t, "first line", equal(c, `{"a":1}`))
	stop()
	stop() // idempotent
	appendTo(t, f, "{\"b\":2}\n")
	time.Sleep(15 * fast.Interval)
	if got := c.snapshot(); !reflect.DeepEqual(got, []string{`{"a":1}`}) {
		t.Fatalf("lines after stop: %v", got)
	}
}

func TestTailFileHoldsPartialTrailingLineUntilComplete(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, f, "{\"a\":1}\n{\"b\":")
	c := &collector{}
	stop := TailFile(f, c.add, fast)
	defer stop()
	eventually(t, "complete line", equal(c, `{"a":1}`))
	time.Sleep(10 * fast.Interval)
	if got := c.snapshot(); len(got) != 1 {
		t.Fatalf("partial line delivered early: %v", got)
	}
	appendTo(t, f, "2}\r\n")
	eventually(t, "completed line", equal(c, `{"a":1}`, `{"b":2}`))
}

func TestTailFileStopWaitsForGoroutineExit(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, f, "{\"a\":1}\n")
	var mu sync.Mutex
	inCallback := false
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	stop := TailFile(f, func(string) {
		mu.Lock()
		inCallback = true
		mu.Unlock()
		entered <- struct{}{}
		<-release
		mu.Lock()
		inCallback = false
		mu.Unlock()
	}, fast)
	<-entered
	go func() { time.Sleep(30 * time.Millisecond); close(release) }()
	stop()
	mu.Lock()
	defer mu.Unlock()
	if inCallback {
		t.Fatal("stop returned while the callback was still running")
	}
}
