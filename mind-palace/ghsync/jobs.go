package ghsync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// JobKind says what a worker does with a job.
type JobKind string

const (
	// JobDelivery applies one webhook delivery (Job.Delivery).
	JobDelivery JobKind = "delivery"
	// JobImport fully imports a repository (Job.Ref empty: every branch the
	// policy selects) or one ref of it.
	JobImport JobKind = "import"
	// JobReconcile compares each tracked branch's tip with the stored head
	// and re-imports drifted refs (nightly).
	JobReconcile JobKind = "reconcile"
	// JobSweep stops syncing branches that turned inactive (daily).
	JobSweep JobKind = "sweep"
)

// Valid reports whether k is one of the known kinds.
func (k JobKind) Valid() bool {
	switch k {
	case JobDelivery, JobImport, JobReconcile, JobSweep:
		return true
	}
	return false
}

// MaxAttempts is how many failed attempts (Retry calls and expired leases)
// a job gets before it is dead-lettered.
const MaxAttempts = 10

// Back-off bounds: the first retry waits BackoffBase, each later one twice
// the previous, never more than BackoffMax.
const (
	BackoffBase = 30 * time.Second
	BackoffMax  = time.Hour
)

// Backoff is the wait before the next try of a job that has failed
// attempts times (1 for the first failure): exponential from BackoffBase,
// capped at BackoffMax.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := BackoffBase
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= BackoffMax {
			return BackoffMax
		}
	}
	return d
}

// Job is one unit of work in the queue.
type Job struct {
	// ID is assigned by Enqueue.
	ID string `json:"id"`
	// Kind is what to do.
	Kind JobKind `json:"kind"`
	// Installation is the GitHub installation id (required).
	Installation int64 `json:"installation"`
	// Repository is GitHub's repository id; 0 for installation-wide work
	// (installation created: list and import its repositories).
	Repository int64 `json:"repository,omitempty"`
	// Ref limits an import to one ref (refs/heads/<branch>).
	Ref string `json:"ref,omitempty"`
	// Delivery is the delivery a JobDelivery applies.
	Delivery *Delivery `json:"delivery,omitempty"`
	// Attempts counts failed attempts so far (Retry calls and expired
	// leases; a Pause is not one).
	Attempts int `json:"attempts"`
	// EnqueuedAt is set by Enqueue.
	EnqueuedAt time.Time `json:"enqueuedAt"`
	// NotBefore is when the job may next be claimed (zero: now).
	NotBefore time.Time `json:"notBefore,omitempty"`
	// LastError is the message of the last failed attempt, for the
	// Integrations page; it never holds a token or a payload.
	LastError string `json:"lastError,omitempty"`
}

// OrderKey is the key jobs are ordered under: the repository, or the
// installation for installation-wide jobs. Jobs with one key run one at a
// time, in the order they were enqueued.
func (j Job) OrderKey() string {
	if j.Repository != 0 {
		return "r" + strconv.FormatInt(j.Repository, 10)
	}
	return "i" + strconv.FormatInt(j.Installation, 10)
}

// Validate checks what Enqueue requires.
func (j Job) Validate() error {
	switch {
	case !j.Kind.Valid():
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidJob, j.Kind)
	case j.Installation <= 0:
		return fmt.Errorf("%w: installation is required", ErrInvalidJob)
	case j.Repository < 0:
		return fmt.Errorf("%w: negative repository", ErrInvalidJob)
	case j.Kind == JobDelivery && j.Delivery == nil:
		return fmt.Errorf("%w: a delivery job needs its delivery", ErrInvalidJob)
	}
	return nil
}

// Lease is a claimed job: the worker owns it until Until, and ends it with
// Done, Retry or Pause before then. A lease that runs out makes the job
// claimable again (counted as a failed attempt), and the old lease's Token
// is then refused with ErrLeaseLost.
type Lease struct {
	Job   Job       `json:"job"`
	Token string    `json:"token"`
	Until time.Time `json:"until"`
}

// Errors of the Jobs port.
var (
	// ErrNoJob: nothing is ready to be claimed.
	ErrNoJob = errors.New("ghsync: no job ready")
	// ErrLeaseLost: the lease ran out (or the job was finished) and the
	// job may be held by another worker.
	ErrLeaseLost = errors.New("ghsync: job lease lost")
	// ErrDeadLettered: Retry used the job's last attempt; it was moved to
	// the dead letters and will not run again (the nightly reconcile
	// re-imports what it would have done).
	ErrDeadLettered = errors.New("ghsync: job dead-lettered")
	// ErrInvalidJob: Enqueue refused the job.
	ErrInvalidJob = errors.New("ghsync: invalid job")
)

// Jobs is the job queue port. Implementations: MemJobs (tests, the
// in-memory server) and clued-cloud's Mongo queue. Every implementation
// keeps these rules:
//
//   - Per-repository order: jobs with one OrderKey are claimed in the order
//     they were enqueued, and never two at once; a job waiting for its
//     retry (or a paused installation) holds back the later jobs of its key.
//   - Jobs of different keys run in parallel.
//   - A lease expires at Until; the job is then claimable again with one
//     more failed attempt, or dead-lettered when that was the last.
type Jobs interface {
	// Enqueue appends a job (Validate must pass) and returns its id.
	Enqueue(ctx context.Context, job Job) (string, error)
	// Claim leases the oldest ready job for lease, or returns ErrNoJob.
	Claim(ctx context.Context, lease time.Duration) (Lease, error)
	// Extend moves the lease's Until to now plus lease and returns the
	// lease to use from here on; ErrLeaseLost when it is no longer current.
	// A worker extends while a job runs, so a long job is not claimed by a
	// second worker while the first still works on it.
	Extend(ctx context.Context, lease Lease, d time.Duration) (Lease, error)
	// Done removes a finished job.
	Done(ctx context.Context, lease Lease) error
	// Retry records a failed attempt (cause's message, never a payload)
	// and makes the job claimable again after after; when that was attempt
	// MaxAttempts it dead-letters the job and returns ErrDeadLettered.
	Retry(ctx context.Context, lease Lease, after time.Duration, cause error) error
	// Pause gives the job back without counting an attempt and holds every
	// job of the lease's installation until until (a GitHub rate limit).
	Pause(ctx context.Context, lease Lease, until time.Time) error
}
