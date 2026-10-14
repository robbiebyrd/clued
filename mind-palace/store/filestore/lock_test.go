package filestore

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLockIsExclusiveAndReleased(t *testing.T) {
	s := New("files", map[string]string{"story": filepath.Join(t.TempDir(), "stories")})
	ctx := context.Background()
	unlock, err := s.Lock(ctx, "story", "0001-abc")
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := s.Lock(short, "story", "0001-abc"); err == nil {
		t.Fatal("a second lock was granted while the first was held")
	}
	if u, err := s.Lock(ctx, "story", "0002-def"); err != nil {
		t.Fatalf("another document: %v", err)
	} else {
		u()
	}
	unlock()
	again, err := s.Lock(ctx, "story", "0001-abc")
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
	if _, err := s.Lock(ctx, "story", "../escape"); err == nil {
		t.Error("an id with a path separator was locked")
	}
}

func TestLockTakesOverAStaleLock(t *testing.T) {
	s := New("files", map[string]string{"story": filepath.Join(t.TempDir(), "stories")})
	ctx := context.Background()
	unlock, err := s.Lock(ctx, "story", "0003-ghi")
	if err != nil {
		t.Fatal(err)
	}
	_ = unlock // the holder "dies" without unlocking
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "mind-palace-locks", "*", "story-0003-ghi.lock"))
	old := time.Now().Add(-2 * staleLock)
	for _, m := range matches {
		_ = os.Chtimes(m, old, old)
	}
	short, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	u, err := s.Lock(short, "story", "0003-ghi")
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	u()
}

func TestLockSerialisesConcurrentHolders(t *testing.T) {
	s := New("files", map[string]string{"story": filepath.Join(t.TempDir(), "stories")})
	ctx := context.Background()
	var mu sync.Mutex
	inside, maxInside := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := s.Lock(ctx, "story", "0004-jkl")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inside++
			maxInside = max(maxInside, inside)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Errorf("%d holders at once, want 1", maxInside)
	}
}
