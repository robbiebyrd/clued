package codesync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testID = "wt_0123456789abcdef"

func TestWorktreeID(t *testing.T) {
	a := WorktreeID("host", "/src/repo")
	if !ValidWorktreeID(a) {
		t.Fatalf("WorktreeID produced an invalid id %q", a)
	}
	if a != WorktreeID("host", "/src/repo") {
		t.Fatal("WorktreeID is not deterministic")
	}
	if a == WorktreeID("other", "/src/repo") || a == WorktreeID("host", "/src/repo-wt") {
		t.Fatal("different host or root gave the same id")
	}
	// The NUL separator keeps host and root apart.
	if WorktreeID("ab", "c") == WorktreeID("a", "bc") {
		t.Fatal("host/root boundary is ambiguous")
	}
}

func TestValidWorktreeID(t *testing.T) {
	for _, id := range []string{"", "wt_", "wt_0123456789abcde", "wt_0123456789abcdef0", "wt_0123456789ABCDEF",
		"xx_0123456789abcdef", "wt_../../../etc/pas", "wt_0123456789abcd/f"} {
		if ValidWorktreeID(id) {
			t.Errorf("ValidWorktreeID(%q) = true", id)
		}
		if _, err := StatePath("d", id); !errors.Is(err, ErrBadWorktreeID) {
			t.Errorf("StatePath(%q) err = %v", id, err)
		}
		if _, err := LockPath("d", id); !errors.Is(err, ErrBadWorktreeID) {
			t.Errorf("LockPath(%q) err = %v", id, err)
		}
	}
	p, err := StatePath("d", testID)
	if err != nil || p != filepath.Join("d", testID+".json") {
		t.Fatalf("StatePath = %q, %v", p, err)
	}
	p, err = LockPath("d", testID)
	if err != nil || p != filepath.Join("d", testID+".lock") {
		t.Fatalf("LockPath = %q, %v", p, err)
	}
	if StateDir("/cfg") != filepath.Join("/cfg", "codesync") {
		t.Fatalf("StateDir = %q", StateDir("/cfg"))
	}
}

func TestLoadStateMissingIsZero(t *testing.T) {
	st, err := LoadState(filepath.Join(t.TempDir(), "nope"), testID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st, State{}) {
		t.Fatalf("missing state = %+v", st)
	}
}

func sampleState() State {
	mt := time.Date(2026, 10, 19, 12, 0, 0, 123456789, time.UTC)
	return State{
		Worktree:      testID,
		Root:          "/src/repo",
		Project:       "github.com__owner__repo",
		DefaultBranch: "main",
		Ref:           "feature",
		Head:          "abc123",
		Upstream:      "def456",
		Ahead:         2,
		Files: map[string]FileState{
			"a.go":   {Size: 10, MTime: mt, SHA256: "s1", GitOID: "o1", Synced: true},
			"big.go": {Size: 5 << 20, MTime: mt, Dirty: true, Unpushed: true},
		},
		Pending:          Pending{Upserts: []string{"b.go"}, Deletes: []string{"c.go"}},
		LastFull:         mt,
		Refused:          map[string]string{"huge.bin": Stamp(9, mt)},
		UnsupportedUntil: mt.Add(BackoffFor),
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg", DirName)
	want := sampleState()
	if err := SaveState(dir, testID, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(dir, testID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, testID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("state file mode = %v, want 0600", fi.Mode().Perm())
		}
		di, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("state dir mode = %v, want 0700", di.Mode().Perm())
		}
	}
}

func TestStateJSONFieldNames(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, testID, sampleState()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, testID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"worktree", "root", "project", "defaultBranch", "ref", "head", "upstream", "ahead",
		"files", "pending", "lastFull", "refused", "unsupportedUntil"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("state JSON lacks %q: %s", k, data)
		}
	}
	var files map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw["files"], &files); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"size", "mtime", "sha256", "gitOid", "dirty", "unpushed", "synced"} {
		if _, ok := files["a.go"][k]; !ok {
			t.Errorf("file entry lacks %q: %s", k, raw["files"])
		}
	}
	// Zero times are left out rather than written as year 1.
	if err := SaveState(dir, testID, State{Worktree: testID}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, testID+".json"))
	if strings.Contains(string(data), "lastFull") || strings.Contains(string(data), "unsupportedUntil") {
		t.Fatalf("zero times written: %s", data)
	}
}

func TestSaveStateIsAtomic(t *testing.T) {
	dir := t.TempDir()
	first := sampleState()
	if err := SaveState(dir, testID, first); err != nil {
		t.Fatal(err)
	}

	// Readers running alongside repeated saves always see a whole state.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad atomic.Value
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				st, err := LoadState(dir, testID)
				if err != nil {
					bad.Store(err)
					return
				}
				if st.Worktree != testID {
					bad.Store(errors.New("read a partial state"))
					return
				}
			}
		}()
	}
	for i := range 50 {
		st := sampleState()
		st.Ahead = i
		if err := SaveState(dir, testID, st); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if v := bad.Load(); v != nil {
		t.Fatal(v)
	}

	// No temporary file is left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != testID+".json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v", names)
	}
}

func TestSaveStateFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, testID, sampleState()); err != nil {
		t.Fatal(err)
	}
	// A directory in the way of the rename makes the save fail; the
	// temporary file is cleaned up.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(filepath.Join(blocked, testID+".json", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(blocked, testID, sampleState()); err == nil {
		t.Fatal("expected SaveState to fail when the target is a non-empty directory")
	}
	entries, _ := os.ReadDir(blocked)
	if len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
	if err := SaveState(dir, "bad", State{}); !errors.Is(err, ErrBadWorktreeID) {
		t.Fatalf("SaveState with a bad id: %v", err)
	}
	got, err := LoadState(dir, testID)
	if err != nil || got.Worktree != testID {
		t.Fatalf("old state lost: %+v, %v", got, err)
	}
}

func TestLoadStateCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, testID+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(dir, testID); err == nil || !strings.Contains(err.Error(), "code sync state") {
		t.Fatalf("corrupt state err = %v", err)
	}
	if _, err := LoadState(dir, "../x"); !errors.Is(err, ErrBadWorktreeID) {
		t.Fatalf("LoadState with a bad id: %v", err)
	}
}

func TestRemoveState(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, testID, sampleState()); err != nil {
		t.Fatal(err)
	}
	if err := RemoveState(dir, testID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, testID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state still there: %v", err)
	}
	if err := RemoveState(dir, testID); err != nil {
		t.Fatalf("removing a missing state: %v", err)
	}
	if err := RemoveState(dir, "nope"); !errors.Is(err, ErrBadWorktreeID) {
		t.Fatalf("RemoveState with a bad id: %v", err)
	}
}

func TestPendingQueue(t *testing.T) {
	var s State
	if !s.Pending.Empty() {
		t.Fatal("zero pending not empty")
	}
	s.QueueUpsert("a")
	s.QueueUpsert("a")
	s.QueueUpsert("b")
	s.QueueDelete("c")
	if !reflect.DeepEqual(s.Pending, Pending{Upserts: []string{"a", "b"}, Deletes: []string{"c"}}) {
		t.Fatalf("pending = %+v", s.Pending)
	}
	// A delete replaces a queued upsert of the same path, and back.
	s.QueueDelete("a")
	s.QueueUpsert("c")
	if !reflect.DeepEqual(s.Pending, Pending{Upserts: []string{"b", "c"}, Deletes: []string{"a"}}) {
		t.Fatalf("pending = %+v", s.Pending)
	}
	s.Dequeue("a")
	s.Dequeue("b")
	s.Dequeue("c")
	if !s.Pending.Empty() || s.Pending.Upserts != nil || s.Pending.Deletes != nil {
		t.Fatalf("pending after dequeue = %+v", s.Pending)
	}
}

func TestRefused(t *testing.T) {
	var s State
	mt := time.Unix(100, 5)
	st := Stamp(42, mt)
	if st != "42+100000000005" {
		t.Fatalf("Stamp = %q", st)
	}
	if (FileState{Size: 42, MTime: mt}).Stamp() != st {
		t.Fatal("FileState.Stamp differs from Stamp")
	}
	if s.IsRefused("x", st) {
		t.Fatal("unknown path refused")
	}
	s.Refuse("x", st)
	if !s.IsRefused("x", st) {
		t.Fatal("refused path with the same stamp not refused")
	}
	// A changed file is tried again and its refusal dropped.
	if s.IsRefused("x", Stamp(43, mt)) {
		t.Fatal("changed stamp still refused")
	}
	if s.Refused != nil {
		t.Fatalf("refusal kept: %v", s.Refused)
	}
}

func TestBackoffAndFullDue(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	var s State
	if s.BackedOff(now) {
		t.Fatal("zero state backed off")
	}
	s.BackOff(now)
	if !s.BackedOff(now.Add(BackoffFor-time.Second)) || s.BackedOff(now.Add(BackoffFor)) {
		t.Fatalf("back-off until %v", s.UnsupportedUntil)
	}
	if !s.FullDue(now) {
		t.Fatal("never reconciled but full not due")
	}
	s.LastFull = now
	if s.FullDue(now.Add(FullAfter-time.Minute)) || !s.FullDue(now.Add(FullAfter)) {
		t.Fatal("full reconcile age not respected")
	}
}

func TestAcquireLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	release, held, err := AcquireLock(dir, testID)
	if err != nil || held {
		t.Fatalf("first lock: held=%v err=%v", held, err)
	}
	lock := filepath.Join(dir, testID+".lock")
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(lock)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("lock mode = %v", fi.Mode().Perm())
		}
	}

	// A second run sees it held, and its release leaves the lock alone.
	release2, held, err := AcquireLock(dir, testID)
	if err != nil || !held {
		t.Fatalf("second lock: held=%v err=%v", held, err)
	}
	release2()
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("held release removed the lock: %v", err)
	}

	// Another worktree is not blocked.
	other := WorktreeID("h", "/other")
	r3, held, err := AcquireLock(dir, other)
	if err != nil || held {
		t.Fatalf("other worktree: held=%v err=%v", held, err)
	}
	r3()

	release()
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock not released: %v", err)
	}
	r4, held, err := AcquireLock(dir, testID)
	if err != nil || held {
		t.Fatalf("lock after release: held=%v err=%v", held, err)
	}
	r4()

	if _, _, err := AcquireLock(dir, "wt_nope"); !errors.Is(err, ErrBadWorktreeID) {
		t.Fatalf("bad id: %v", err)
	}
}

func TestAcquireLockTakesOverStale(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, testID+".lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Fresh: held.
	if _, held, err := AcquireLock(dir, testID); err != nil || !held {
		t.Fatalf("fresh lock: held=%v err=%v", held, err)
	}
	// Just short of stale: still held.
	young := time.Now().Add(-StaleAfter + 5*time.Second)
	if err := os.Chtimes(lock, young, young); err != nil {
		t.Fatal(err)
	}
	if _, held, err := AcquireLock(dir, testID); err != nil || !held {
		t.Fatalf("young lock: held=%v err=%v", held, err)
	}
	// Older than StaleAfter: taken over.
	old := time.Now().Add(-StaleAfter - time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	release, held, err := AcquireLock(dir, testID)
	if err != nil || held {
		t.Fatalf("stale lock: held=%v err=%v", held, err)
	}
	fi, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(fi.ModTime()) > StaleAfter/2 {
		t.Fatal("stale lock not replaced by a fresh one")
	}
	release()
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestAcquireLockConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	var winners atomic.Int32
	start := make(chan struct{})
	releases := make(chan func(), n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, held, err := AcquireLock(dir, testID)
			if err != nil {
				t.Error(err)
				return
			}
			if !held {
				winners.Add(1)
			}
			releases <- release
		}()
	}
	close(start)
	wg.Wait()
	close(releases)
	if got := winners.Load(); got != 1 {
		t.Fatalf("%d runs took the lock, want 1", got)
	}
	for r := range releases {
		r()
	}
	if _, err := os.Stat(filepath.Join(dir, testID+".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock left after every release: %v", err)
	}
}

func TestAcquireLockDirError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, held, err := AcquireLock(filepath.Join(file, "sub"), testID); err == nil || held {
		t.Fatalf("lock under a file: held=%v err=%v", held, err)
	}
}
