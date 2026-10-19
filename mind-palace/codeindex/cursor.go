package codeindex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// CursorCollection is the MongoDB collection holding one cursor document per
// namespaced project.
const CursorCollection = "code_index_cursors"

// Errors a Cursors returns, matched with errors.Is.
var (
	// ErrLeased: another holder's lease on the project is current.
	ErrLeased = errors.New("codeindex: project leased by another worker")
	// ErrLeaseLost: the lease expired or was taken over; the holder must
	// stop writing and drop what it computed since its last Save.
	ErrLeaseLost = errors.New("codeindex: lease lost")
)

// Cursor is one project's position in its change log plus what the indexer
// learned from the log that the code store does not keep: the head and
// upstream commit of every (worktree, ref) tree and when each upstream was
// first seen, which orders upstream commits without git. It is saved with
// the lease, so a worker that takes the project over resumes exactly where
// the last Save left it.
type Cursor struct {
	// Project is the namespaced project (u<user>__<slug>, w<ws>__<slug>).
	Project string `json:"project"`
	// Seq is the last change applied; Changes(Seq) is the next batch.
	Seq int64 `json:"seq"`
	// Trees are the trees seen in the log, ordered by worktree then ref.
	Trees []TreeState `json:"trees,omitempty"`
	// UpdatedAt is the last Save.
	UpdatedAt time.Time `json:"updatedAt"`
}

// TreeState is the change log's latest word on one (worktree, ref) tree.
type TreeState struct {
	Worktree string `json:"worktree"`
	Ref      string `json:"ref"`
	// Owner is the account that wrote the tree's last change.
	Owner string `json:"owner,omitempty"`
	// Head and Upstream are the last change's HEAD and upstream commits;
	// Upstream is empty for a branch that was never pushed.
	Head     string `json:"head,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	// UpstreamSeq is the Seq of the change that first reported Upstream
	// for this tree: the larger, the newer the upstream commit ("newest
	// upstream commit wins" for the base).
	UpstreamSeq int64 `json:"upstreamSeq,omitempty"`
	// Seq is the tree's last change.
	Seq int64 `json:"seq"`
}

// Key names the tree.
func (t TreeState) Key() TreeKey { return TreeKey{Worktree: t.Worktree, Ref: t.Ref} }

// TreeKey names one (worktree, ref) tree.
type TreeKey struct {
	Worktree string `json:"worktree"`
	Ref      string `json:"ref"`
}

// Lease is a worker's hold on one project. Only the holder of a current
// lease may Save the project's cursor, so one worker at a time indexes it.
type Lease struct {
	Project string    `json:"project"`
	Holder  string    `json:"holder"`
	Token   string    `json:"token"`
	Until   time.Time `json:"until"`
}

// Cursors stores the per-project cursors and leases (code_index_cursors).
// Implementations are safe for concurrent use and for several processes
// sharing one store (MongoCursors).
type Cursors interface {
	// Acquire takes the project's lease for holder until now+ttl and
	// returns it with the project's cursor (Seq 0 and no trees for a
	// project never indexed). ErrLeased when another lease is current; the
	// same holder re-acquiring gets a fresh lease.
	Acquire(ctx context.Context, project, holder string, ttl time.Duration) (Lease, Cursor, error)
	// Save stores c (c.Project must be the lease's) and extends the lease
	// to now+ttl, returning the extended lease. ErrLeaseLost when the lease
	// is no longer current.
	Save(ctx context.Context, l Lease, c Cursor, ttl time.Duration) (Lease, error)
	// Release gives the lease up early; releasing a lost lease is not an
	// error.
	Release(ctx context.Context, l Lease) error
	// Delete forgets the project's cursor and lease (a purge, or a rebuild
	// replaying from the start). Deleting a missing cursor is not an error.
	Delete(ctx context.Context, project string) error
}

// newToken returns a random lease token.
func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// cloneCursor copies c's slice so callers never share it with a store.
func cloneCursor(c Cursor) Cursor {
	c.Trees = slices.Clone(c.Trees)
	return c
}

// MemCursors is the in-memory Cursors, for tests and the in-memory server.
type MemCursors struct {
	mu   sync.Mutex
	now  func() time.Time
	docs map[string]*memCursor
}

type memCursor struct {
	cursor Cursor
	lease  Lease
}

var _ Cursors = (*MemCursors)(nil)

// NewMemCursors returns an empty store on the clock now (time.Now when nil).
func NewMemCursors(now func() time.Time) *MemCursors {
	if now == nil {
		now = time.Now
	}
	return &MemCursors{now: now, docs: map[string]*memCursor{}}
}

// Acquire implements Cursors.
func (m *MemCursors) Acquire(ctx context.Context, project, holder string, ttl time.Duration) (Lease, Cursor, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, Cursor{}, err
	}
	if project == "" || holder == "" || ttl <= 0 {
		return Lease{}, Cursor{}, fmt.Errorf("codeindex: acquire needs a project, a holder and a positive ttl")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	d := m.docs[project]
	if d == nil {
		d = &memCursor{cursor: Cursor{Project: project}}
		m.docs[project] = d
	}
	if d.lease.Token != "" && now.Before(d.lease.Until) && d.lease.Holder != holder {
		return Lease{}, Cursor{}, ErrLeased
	}
	d.lease = Lease{Project: project, Holder: holder, Token: newToken(), Until: now.Add(ttl)}
	return d.lease, cloneCursor(d.cursor), nil
}

// current reports whether l is the project's current lease.
func (m *MemCursors) current(l Lease) *memCursor {
	d := m.docs[l.Project]
	if d == nil || d.lease.Token == "" || d.lease.Token != l.Token || !m.now().Before(d.lease.Until) {
		return nil
	}
	return d
}

// Save implements Cursors.
func (m *MemCursors) Save(ctx context.Context, l Lease, c Cursor, ttl time.Duration) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	if c.Project != l.Project {
		return Lease{}, fmt.Errorf("codeindex: cursor of %q saved under the lease of %q", c.Project, l.Project)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.current(l)
	if d == nil {
		return Lease{}, ErrLeaseLost
	}
	now := m.now()
	c = cloneCursor(c)
	c.UpdatedAt = now
	d.cursor = c
	if ttl > 0 {
		d.lease.Until = now.Add(ttl)
	}
	return d.lease, nil
}

// Release implements Cursors.
func (m *MemCursors) Release(ctx context.Context, l Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := m.current(l); d != nil {
		d.lease = Lease{}
	}
	return nil
}

// Delete implements Cursors.
func (m *MemCursors) Delete(ctx context.Context, project string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, project)
	return nil
}

// MongoCursors is the MongoDB Cursors: one document per project in
// code_index_cursors, _id the namespaced project. Leases are compare-and-set
// updates on the document, so workers in several processes (clued server
// replicas, clued-cloud instances) never index one project at once.
type MongoCursors struct {
	coll *mongo.Collection
	now  func() time.Time
}

var _ Cursors = (*MongoCursors)(nil)

// NewMongoCursors returns the cursors in db's collection (CursorCollection
// when collection is empty) on the clock now (time.Now when nil).
func NewMongoCursors(db *mongo.Database, collection string, now func() time.Time) *MongoCursors {
	if collection == "" {
		collection = CursorCollection
	}
	if now == nil {
		now = time.Now
	}
	return &MongoCursors{coll: db.Collection(collection), now: now}
}

// cursorDoc is the stored form of one project's cursor and lease.
type cursorDoc struct {
	ID         string    `bson:"_id"`
	Seq        int64     `bson:"seq"`
	Trees      []treeDoc `bson:"trees,omitempty"`
	UpdatedAt  time.Time `bson:"updated_at,omitempty"`
	Holder     string    `bson:"holder,omitempty"`
	Token      string    `bson:"token,omitempty"`
	LeaseUntil time.Time `bson:"lease_until,omitempty"`
}

type treeDoc struct {
	Worktree    string `bson:"worktree"`
	Ref         string `bson:"ref"`
	Owner       string `bson:"owner,omitempty"`
	Head        string `bson:"head,omitempty"`
	Upstream    string `bson:"upstream,omitempty"`
	UpstreamSeq int64  `bson:"upstream_seq,omitempty"`
	Seq         int64  `bson:"seq"`
}

func (d cursorDoc) cursor() Cursor {
	c := Cursor{Project: d.ID, Seq: d.Seq, UpdatedAt: d.UpdatedAt}
	for _, t := range d.Trees {
		c.Trees = append(c.Trees, TreeState(t))
	}
	return c
}

func treeDocs(ts []TreeState) []treeDoc {
	out := make([]treeDoc, 0, len(ts))
	for _, t := range ts {
		out = append(out, treeDoc(t))
	}
	return out
}

// mongoTime is t in UTC at MongoDB's millisecond precision, so a lease
// read back compares equal to the one written.
func mongoTime(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }

// Acquire implements Cursors: one conditional upsert that matches the
// project's document only when its lease is free, expired or the holder's
// own. When it matches nothing the upsert's insert collides with the
// existing document's _id, which is how a current lease shows.
func (m *MongoCursors) Acquire(ctx context.Context, project, holder string, ttl time.Duration) (Lease, Cursor, error) {
	if project == "" || holder == "" || ttl <= 0 {
		return Lease{}, Cursor{}, fmt.Errorf("codeindex: acquire needs a project, a holder and a positive ttl")
	}
	now := mongoTime(m.now())
	l := Lease{Project: project, Holder: holder, Token: newToken(), Until: mongoTime(now.Add(ttl))}
	filter := bson.D{
		{Key: "_id", Value: project},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}},
			bson.D{{Key: "lease_until", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "holder", Value: holder}},
		}},
	}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "holder", Value: l.Holder}, {Key: "token", Value: l.Token}, {Key: "lease_until", Value: l.Until},
	}}, {Key: "$setOnInsert", Value: bson.D{{Key: "seq", Value: int64(0)}}}}
	var d cursorDoc
	err := m.coll.FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&d)
	if mongo.IsDuplicateKeyError(err) {
		return Lease{}, Cursor{}, ErrLeased
	}
	if err != nil {
		return Lease{}, Cursor{}, fmt.Errorf("codeindex: acquire %s: %w", project, err)
	}
	return l, d.cursor(), nil
}

// leaseFilter matches the project's document while l is its current lease.
func leaseFilter(l Lease, now time.Time) bson.D {
	return bson.D{
		{Key: "_id", Value: l.Project},
		{Key: "token", Value: l.Token},
		{Key: "lease_until", Value: bson.D{{Key: "$gt", Value: now}}},
	}
}

// Save implements Cursors.
func (m *MongoCursors) Save(ctx context.Context, l Lease, c Cursor, ttl time.Duration) (Lease, error) {
	if c.Project != l.Project {
		return Lease{}, fmt.Errorf("codeindex: cursor of %q saved under the lease of %q", c.Project, l.Project)
	}
	now := mongoTime(m.now())
	set := bson.D{{Key: "seq", Value: c.Seq}, {Key: "trees", Value: treeDocs(c.Trees)}, {Key: "updated_at", Value: now}}
	if ttl > 0 {
		l.Until = mongoTime(now.Add(ttl))
		set = append(set, bson.E{Key: "lease_until", Value: l.Until})
	}
	res, err := m.coll.UpdateOne(ctx, leaseFilter(l, now), bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return Lease{}, fmt.Errorf("codeindex: save %s: %w", l.Project, err)
	}
	if res.MatchedCount == 0 {
		return Lease{}, ErrLeaseLost
	}
	return l, nil
}

// Release implements Cursors.
func (m *MongoCursors) Release(ctx context.Context, l Lease) error {
	_, err := m.coll.UpdateOne(ctx, bson.D{{Key: "_id", Value: l.Project}, {Key: "token", Value: l.Token}},
		bson.D{{Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "token", Value: ""}, {Key: "lease_until", Value: ""}}}})
	if err != nil {
		return fmt.Errorf("codeindex: release %s: %w", l.Project, err)
	}
	return nil
}

// Delete implements Cursors.
func (m *MongoCursors) Delete(ctx context.Context, project string) error {
	if _, err := m.coll.DeleteOne(ctx, bson.D{{Key: "_id", Value: project}}); err != nil {
		return fmt.Errorf("codeindex: delete cursor %s: %w", project, err)
	}
	return nil
}
