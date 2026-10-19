package ghsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"
)

// MemJobs is the in-memory Jobs: for tests and the in-memory server. It
// keeps the port's ordering, lease and dead-letter rules; nothing survives
// the process.
type MemJobs struct {
	mu     sync.Mutex
	now    func() time.Time
	seq    int64
	queue  []*memJob // enqueue order
	paused map[int64]time.Time
	dead   []Job
}

type memJob struct {
	job   Job
	token string    // current lease token, "" when not leased
	until time.Time // lease expiry
}

// NewMemJobs returns an empty queue on the clock now (time.Now when nil).
func NewMemJobs(now func() time.Time) *MemJobs {
	if now == nil {
		now = time.Now
	}
	return &MemJobs{now: now, paused: map[int64]time.Time{}}
}

var _ Jobs = (*MemJobs)(nil)

// Enqueue implements Jobs.
func (m *MemJobs) Enqueue(ctx context.Context, job Job) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := job.Validate(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	job.ID = "job_" + strconv.FormatInt(m.seq, 10)
	job.EnqueuedAt = m.now()
	job.Attempts = 0
	m.queue = append(m.queue, &memJob{job: job})
	return job.ID, nil
}

// Claim implements Jobs.
func (m *MemJobs) Claim(ctx context.Context, lease time.Duration) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.expireLocked(now)
	seen := map[string]bool{} // keys whose head job came before
	for _, j := range m.queue {
		key := j.job.OrderKey()
		if seen[key] {
			continue
		}
		seen[key] = true
		if j.token != "" || now.Before(j.job.NotBefore) {
			continue
		}
		if until, ok := m.paused[j.job.Installation]; ok {
			if now.Before(until) {
				continue
			}
			delete(m.paused, j.job.Installation)
		}
		j.token = newToken()
		j.until = now.Add(lease)
		return Lease{Job: j.job, Token: j.token, Until: j.until}, nil
	}
	return Lease{}, ErrNoJob
}

// expireLocked takes back expired leases, counting a failed attempt each.
func (m *MemJobs) expireLocked(now time.Time) {
	kept := m.queue[:0]
	for _, j := range m.queue {
		if j.token != "" && !now.Before(j.until) {
			j.token = ""
			j.job.Attempts++
			j.job.LastError = "lease expired"
			if j.job.Attempts >= MaxAttempts {
				m.dead = append(m.dead, j.job)
				continue
			}
		}
		kept = append(kept, j)
	}
	clear(m.queue[len(kept):])
	m.queue = kept
}

// find returns the index of the job lease holds, or -1 when the lease is
// no longer current.
func (m *MemJobs) find(l Lease) int {
	now := m.now()
	for i, j := range m.queue {
		if j.job.ID == l.Job.ID {
			if j.token == "" || j.token != l.Token || !now.Before(j.until) {
				return -1
			}
			return i
		}
	}
	return -1
}

func (m *MemJobs) remove(i int) {
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
}

// Done implements Jobs.
func (m *MemJobs) Done(ctx context.Context, l Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.find(l)
	if i < 0 {
		return ErrLeaseLost
	}
	m.remove(i)
	return nil
}

// Retry implements Jobs.
func (m *MemJobs) Retry(ctx context.Context, l Lease, after time.Duration, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.find(l)
	if i < 0 {
		return ErrLeaseLost
	}
	j := m.queue[i]
	j.token = ""
	j.job.Attempts++
	j.job.LastError = ""
	if cause != nil {
		j.job.LastError = cause.Error()
	}
	if j.job.Attempts >= MaxAttempts {
		m.dead = append(m.dead, j.job)
		m.remove(i)
		return ErrDeadLettered
	}
	j.job.NotBefore = m.now().Add(after)
	return nil
}

// Pause implements Jobs.
func (m *MemJobs) Pause(ctx context.Context, l Lease, until time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.find(l)
	if i < 0 {
		return ErrLeaseLost
	}
	j := m.queue[i]
	j.token = ""
	j.job.NotBefore = until
	if cur, ok := m.paused[j.job.Installation]; !ok || until.After(cur) {
		m.paused[j.job.Installation] = until
	}
	return nil
}

// Len is the number of queued jobs, leased ones included (dead letters
// excluded).
func (m *MemJobs) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// DeadLetters returns the dead-lettered jobs, oldest first.
func (m *MemJobs) DeadLetters() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Job(nil), m.dead...)
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
