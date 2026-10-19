// Package mongocode stores the collected code of one namespaced project in
// MongoDB (code.Store).
//
// Collections, named <prefix><name> like store/mongostore's, where the
// server's prefix names the project (ProjectPrefix):
//
//	code_blobs      _id sha256, size, content
//	code_trees      one entry of a (worktree, ref) tree: _id (entryID),
//	                tree (treeKey; unique with path), worktree (indexed),
//	                ref, path, sha256, size, mode, lang, git_oid (indexed),
//	                dirty, unpushed, session
//	code_worktrees  _id worktree id, owner, source, root, host, origin,
//	                default_branch, refs, sessions, removed, updated_at
//	code_changes    the change log: seq (unique, indexed) and the change
//	code_events     the activity log: seq (unique, indexed) and the event
//	code_counters   the project's sequences (_id "changes" and "events")
//
// Commit, RemoveWorktree, RemoveRef and AppendEvents run in a transaction when the deployment supports
// one (a replica set or a sharded cluster). On a standalone server they order
// their writes so a retried call is idempotent: the tree first, then the
// worktree, then the log entry, so a failure part-way leaves at most entries
// the retry writes again and the retry appends at most a duplicate log entry.
// AppendEvents then reserves its block of seqs before inserting the events,
// so a failure part-way leaves at most a gap in the activity log's seqs.
//
// No caller value reaches a query filter as it was given. Hashes, git object
// ids and worktree ids are rebuilt from their alphabets at the point of use
// (code.CanonicalSHA256, CanonicalGitOID, CanonicalWorktreeID). Refs and
// paths, which may hold any character, never reach a filter at all: a tree
// is found by treeKey and an entry by entryID, hex encodings of them, and a
// full reconcile deletes the _ids it read back from the tree.
package mongocode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/code"
)

// Collection names, after the prefix.
const (
	blobsName     = "code_blobs"
	treesName     = "code_trees"
	worktreesName = "code_worktrees"
	changesName   = "code_changes"
	countersName  = "code_counters"
	eventsName    = "code_events"
)

// The _ids of the counter documents behind Change.Seq and Event.Seq.
const (
	changesCounter = "changes"
	eventsCounter  = "events"
)

// ProjectPrefix returns the collection prefix of a namespaced project: base
// (an operator's configured prefix, often empty) followed by the project and
// "_", as server/projects stamps the mongodb store plugin's collection
// option.
func ProjectPrefix(base, project string) string { return base + project + "_" }

// Option changes how Open sets a store up.
type Option func(*Store)

// WithoutTransactions makes Commit, RemoveWorktree, RemoveRef and
// AppendEvents use the ordered,
// idempotent writes of a standalone server even when the deployment supports
// transactions.
func WithoutTransactions() Option { return func(s *Store) { s.noTx = true } }

// Store is a code.Store over one database and collection prefix.
type Store struct {
	client    *mongo.Client
	blobs     *mongo.Collection
	trees     *mongo.Collection
	worktrees *mongo.Collection
	changes   *mongo.Collection
	counters  *mongo.Collection
	events    *mongo.Collection
	noTx      bool
	tx        bool
}

var _ code.Store = (*Store)(nil)

// Open returns the store whose collections are prefix plus the names above
// in db, creating their indexes, and detects whether the deployment supports
// transactions. The caller owns db's client.
func Open(ctx context.Context, db *mongo.Database, prefix string, opts ...Option) (*Store, error) {
	s := &Store{
		client:    db.Client(),
		blobs:     db.Collection(prefix + blobsName),
		trees:     db.Collection(prefix + treesName),
		worktrees: db.Collection(prefix + worktreesName),
		changes:   db.Collection(prefix + changesName),
		counters:  db.Collection(prefix + countersName),
		events:    db.Collection(prefix + eventsName),
	}
	for _, o := range opts {
		o(s)
	}
	if _, err := s.trees.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tree", Value: 1}, {Key: "path", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "worktree", Value: 1}}},
		{Keys: bson.D{{Key: "git_oid", Value: 1}}},
	}); err != nil {
		return nil, fmt.Errorf("mongocode: index %s: %w", s.trees.Name(), err)
	}
	for _, c := range []*mongo.Collection{s.changes, s.events} {
		if _, err := c.Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: bson.D{{Key: "seq", Value: 1}}, Options: options.Index().SetUnique(true),
		}); err != nil {
			return nil, fmt.Errorf("mongocode: index %s: %w", c.Name(), err)
		}
	}
	// Transactions need the collections to exist on servers before 4.4.
	for _, c := range []*mongo.Collection{s.blobs, s.worktrees, s.counters} {
		if err := db.CreateCollection(ctx, c.Name()); err != nil && !isNamespaceExists(err) {
			return nil, fmt.Errorf("mongocode: create %s: %w", c.Name(), err)
		}
	}
	if !s.noTx {
		tx, err := supportsTransactions(ctx, s.client)
		if err != nil {
			return nil, err
		}
		s.tx = tx
	}
	return s, nil
}

// Transactions reports whether Commit, RemoveWorktree, RemoveRef and
// AppendEvents run in a transaction.
func (s *Store) Transactions() bool { return s.tx }

// supportsTransactions asks the server whether it is a replica set member or
// a mongos, the deployments that run multi-document transactions.
func supportsTransactions(ctx context.Context, client *mongo.Client) (bool, error) {
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return false, fmt.Errorf("mongocode: hello: %w", err)
	}
	return hello.SetName != "" || hello.Msg == "isdbgrid", nil
}

func isNamespaceExists(err error) bool {
	var ce mongo.CommandError
	return errors.As(err, &ce) && (ce.Code == 48 || ce.Name == "NamespaceExists")
}

// Stored forms.

type blobDoc struct {
	SHA256  string `bson:"_id"`
	Size    int64  `bson:"size"`
	Content []byte `bson:"content"`
}

type entryDoc struct {
	Path     string `bson:"path"`
	SHA256   string `bson:"sha256"`
	Size     int64  `bson:"size"`
	Mode     string `bson:"mode"`
	Lang     string `bson:"lang"`
	GitOID   string `bson:"git_oid"`
	Dirty    bool   `bson:"dirty"`
	Unpushed bool   `bson:"unpushed"`
	Session  string `bson:"session"`
}

// treeDoc is one entry of a tree. Its _id is entryID(worktree, ref, path),
// so an upsert by _id never races into the unique (tree, path) index, and
// Tree is treeKey(worktree, ref).
type treeDoc struct {
	ID       string `bson:"_id"`
	Tree     string `bson:"tree"`
	Worktree string `bson:"worktree"`
	Ref      string `bson:"ref"`
	Path     string `bson:"path"`
	SHA256   string `bson:"sha256"`
	Size     int64  `bson:"size"`
	Mode     string `bson:"mode"`
	Lang     string `bson:"lang"`
	GitOID   string `bson:"git_oid"`
	Dirty    bool   `bson:"dirty"`
	Unpushed bool   `bson:"unpushed"`
	Session  string `bson:"session"`
}

func toTreeDoc(worktree, ref string, e entryDoc) treeDoc {
	return treeDoc{ID: entryID(worktree, ref, e.Path), Tree: treeKey(worktree, ref), Worktree: worktree, Ref: ref, Path: e.Path,
		SHA256: e.SHA256, Size: e.Size, Mode: e.Mode, Lang: e.Lang, GitOID: e.GitOID,
		Dirty: e.Dirty, Unpushed: e.Unpushed, Session: e.Session}
}

func (d treeDoc) entry() code.Entry {
	return code.Entry{Path: d.Path, SHA256: d.SHA256, Size: d.Size, Mode: d.Mode, Lang: d.Lang,
		GitOID: d.GitOID, Dirty: d.Dirty, Unpushed: d.Unpushed, Session: d.Session}
}

type sessionDoc struct {
	ID      string    `bson:"id"`
	FirstAt time.Time `bson:"first_at"`
	LastAt  time.Time `bson:"last_at"`
	Ended   bool      `bson:"ended"`
}

type worktreeDoc struct {
	ID            string       `bson:"_id"`
	Owner         string       `bson:"owner"`
	Source        string       `bson:"source"`
	Root          string       `bson:"root"`
	Host          string       `bson:"host"`
	Origin        string       `bson:"origin"`
	DefaultBranch string       `bson:"default_branch"`
	Refs          []string     `bson:"refs"`
	Sessions      []sessionDoc `bson:"sessions"`
	Removed       bool         `bson:"removed"`
	UpdatedAt     time.Time    `bson:"updated_at"`
}

type changeDoc struct {
	Seq       int64      `bson:"seq"`
	Owner     string     `bson:"owner"`
	Worktree  string     `bson:"worktree"`
	Ref       string     `bson:"ref"`
	Head      string     `bson:"head"`
	Upstream  string     `bson:"upstream"`
	Ahead     int        `bson:"ahead"`
	SessionID string     `bson:"session_id"`
	Upserts   []entryDoc `bson:"upserts"`
	Deletes   []string   `bson:"deletes"`
	Full      bool       `bson:"full"`
	At        time.Time  `bson:"at"`
}

type eventDoc struct {
	Seq         int64     `bson:"seq"`
	Kind        string    `bson:"kind"`
	Delivery    string    `bson:"delivery"`
	Worktree    string    `bson:"worktree"`
	Repo        string    `bson:"repo"`
	Ref         string    `bson:"ref"`
	Commit      string    `bson:"commit"`
	Before      string    `bson:"before"`
	Commits     int       `bson:"commits"`
	Forced      bool      `bson:"forced"`
	Actor       string    `bson:"actor"`
	PR          int       `bson:"pr"`
	BaseRef     string    `bson:"base_ref"`
	BaseCommit  string    `bson:"base_commit"`
	Merged      bool      `bson:"merged"`
	MergeCommit string    `bson:"merge_commit"`
	Name        string    `bson:"name"`
	Status      string    `bson:"status"`
	Conclusion  string    `bson:"conclusion"`
	URL         string    `bson:"url"`
	DocKind     string    `bson:"doc_kind"`
	DocID       string    `bson:"doc_id"`
	Path        string    `bson:"path"`
	Action      string    `bson:"action"`
	Problems    []string  `bson:"problems"`
	At          time.Time `bson:"at"`
}

func toEventDoc(e code.Event) eventDoc {
	problems := append([]string{}, e.Problems...)
	return eventDoc{Seq: e.Seq, Kind: e.Kind, Delivery: e.Delivery, Worktree: e.Worktree, Repo: e.Repo,
		Ref: e.Ref, Commit: e.Commit, Before: e.Before, Commits: e.Commits, Forced: e.Forced, Actor: e.Actor,
		PR: e.PR, BaseRef: e.BaseRef, BaseCommit: e.BaseCommit, Merged: e.Merged, MergeCommit: e.MergeCommit,
		Name: e.Name, Status: e.Status, Conclusion: e.Conclusion, URL: e.URL, DocKind: e.DocKind,
		DocID: e.DocID, Path: e.Path, Action: e.Action, Problems: problems, At: e.At}
}

func (d eventDoc) event() code.Event {
	e := code.Event{Seq: d.Seq, Kind: d.Kind, Delivery: d.Delivery, Worktree: d.Worktree, Repo: d.Repo,
		Ref: d.Ref, Commit: d.Commit, Before: d.Before, Commits: d.Commits, Forced: d.Forced, Actor: d.Actor,
		PR: d.PR, BaseRef: d.BaseRef, BaseCommit: d.BaseCommit, Merged: d.Merged, MergeCommit: d.MergeCommit,
		Name: d.Name, Status: d.Status, Conclusion: d.Conclusion, URL: d.URL, DocKind: d.DocKind,
		DocID: d.DocID, Path: d.Path, Action: d.Action, At: d.At}
	if len(d.Problems) > 0 {
		e.Problems = d.Problems
	}
	return e
}

// treeKey names the (worktree, ref) tree in queries: the hex encoding of
// "tree", worktree and ref joined with NUL, which none of them may contain.
func treeKey(worktree, ref string) string { return hexKey("tree", worktree, ref) }

// entryID is the _id of the entry at path in the (worktree, ref) tree: the
// hex encoding of "entry", worktree, ref and path joined with NUL.
func entryID(worktree, ref, path string) string { return hexKey("entry", worktree, ref, path) }

// hexKey returns the hex encoding of parts joined with NUL, rebuilt from
// the hex alphabet so a filter built from it holds nothing else. It is an
// encoding, not a hash, so two different keys never collide.
func hexKey(parts ...string) string {
	id, _ := code.CanonicalHex(hex.EncodeToString([]byte(strings.Join(parts, "\x00"))))
	return id
}

// canonicalWorktree is the worktree id as it may appear in a filter, or
// code.ErrInvalid.
func canonicalWorktree(id string) (string, error) {
	c, ok := code.CanonicalWorktreeID(id)
	if !ok {
		return "", invalid("worktree id %q", id)
	}
	return c, nil
}

// deleteBatch bounds the _ids of one DeleteMany, well under the 16 MB
// command limit (a variable so a test can lower it).
var deleteBatch = 10000

// deleteIDs deletes the tree entries with those _ids, in batches.
func (s *Store) deleteIDs(ctx context.Context, ids []string) error {
	for len(ids) > 0 {
		n := min(len(ids), deleteBatch)
		if _, err := s.trees.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids[:n]}}); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

func toEntryDoc(e code.Entry) entryDoc {
	return entryDoc{Path: e.Path, SHA256: e.SHA256, Size: e.Size, Mode: e.Mode, Lang: e.Lang,
		GitOID: e.GitOID, Dirty: e.Dirty, Unpushed: e.Unpushed, Session: e.Session}
}

func (d entryDoc) entry() code.Entry {
	return code.Entry{Path: d.Path, SHA256: d.SHA256, Size: d.Size, Mode: d.Mode, Lang: d.Lang,
		GitOID: d.GitOID, Dirty: d.Dirty, Unpushed: d.Unpushed, Session: d.Session}
}

func (d worktreeDoc) worktree() code.Worktree {
	w := code.Worktree{ID: d.ID, Owner: d.Owner, Source: d.Source, Root: d.Root, Host: d.Host,
		Origin: d.Origin, DefaultBranch: d.DefaultBranch, Refs: d.Refs, Removed: d.Removed,
		UpdatedAt: d.UpdatedAt}
	for _, ss := range d.Sessions {
		w.Sessions = append(w.Sessions, code.SessionSeen{ID: ss.ID, FirstAt: ss.FirstAt, LastAt: ss.LastAt, Ended: ss.Ended})
	}
	return w
}

func (d changeDoc) change() code.Change {
	c := code.Change{Seq: d.Seq, Owner: d.Owner, Worktree: d.Worktree, Ref: d.Ref, Head: d.Head,
		Upstream: d.Upstream, Ahead: d.Ahead, SessionID: d.SessionID, Deletes: d.Deletes, Full: d.Full, At: d.At}
	for _, e := range d.Upserts {
		c.Upserts = append(c.Upserts, e.entry())
	}
	return c
}

// Validation, at the point of use.

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", code.ErrInvalid, fmt.Sprintf(format, args...))
}

// validGitOIDOrEmpty is the rule for Entry.GitOID, Change.Head and
// Change.Upstream.
func validGitOIDOrEmpty(s string) bool { return s == "" || code.ValidGitOID(s) }

func validateChange(c code.Change) error {
	if !code.ValidWorktreeID(c.Worktree) {
		return invalid("worktree id %q", c.Worktree)
	}
	if !code.ValidRef(c.Ref) {
		return invalid("ref %q", c.Ref)
	}
	if !validGitOIDOrEmpty(c.Head) {
		return invalid("head %q", c.Head)
	}
	if !validGitOIDOrEmpty(c.Upstream) {
		return invalid("upstream %q", c.Upstream)
	}
	if c.Ahead < 0 {
		return invalid("ahead %d", c.Ahead)
	}
	for _, e := range c.Upserts {
		if !code.ValidPath(e.Path) {
			return invalid("path %q", e.Path)
		}
		if e.SHA256 != "" && !code.ValidSHA256(e.SHA256) {
			return invalid("sha256 %q of %q", e.SHA256, e.Path)
		}
		if !validGitOIDOrEmpty(e.GitOID) {
			return invalid("git oid %q of %q", e.GitOID, e.Path)
		}
		if e.Size < 0 {
			return invalid("size %d of %q", e.Size, e.Path)
		}
	}
	for _, p := range c.Deletes {
		if !code.ValidPath(p) {
			return invalid("delete path %q", p)
		}
	}
	return nil
}

// Blobs.

// MissingBlobs returns the hashes in shas the store does not hold, in the
// order given.
func (s *Store) MissingBlobs(ctx context.Context, shas []string) ([]string, error) {
	for _, sha := range shas {
		if !code.ValidSHA256(sha) {
			return nil, invalid("sha256 %q", sha)
		}
	}
	return s.missing(ctx, shas)
}

// missing returns the hashes in shas (valid SHA-256s) the store does not
// hold, in the order given.
func (s *Store) missing(ctx context.Context, shas []string) ([]string, error) {
	if len(shas) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(shas))
	for _, sha := range shas {
		id, ok := code.CanonicalSHA256(sha)
		if !ok {
			return nil, invalid("sha256 %q", sha)
		}
		ids = append(ids, id)
	}
	cur, err := s.blobs.Find(ctx, bson.M{"_id": bson.M{"$in": ids}}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	var held []struct {
		ID string `bson:"_id"`
	}
	if err := cur.All(ctx, &held); err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(held))
	for _, h := range held {
		have[h.ID] = true
	}
	var out []string
	for _, id := range ids {
		if !have[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// PutBlob stores b after verifying its hash and size; a blob already held is
// left as it is.
func (s *Store) PutBlob(ctx context.Context, b code.Blob) error {
	id, ok := code.CanonicalSHA256(b.SHA256)
	if !ok {
		return invalid("sha256 %q", b.SHA256)
	}
	if b.Size != int64(len(b.Content)) {
		return invalid("size %d of a %d-byte blob", b.Size, len(b.Content))
	}
	sum := sha256.Sum256(b.Content)
	if hex.EncodeToString(sum[:]) != id {
		return invalid("content does not hash to %s", id)
	}
	content := b.Content
	if content == nil {
		content = []byte{}
	}
	_, err := s.blobs.UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$setOnInsert": bson.M{"size": b.Size, "content": content}},
		options.UpdateOne().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) { // a concurrent put of the same blob
		return nil
	}
	return err
}

// Blob returns the blob with that SHA-256, or code.ErrNotFound.
func (s *Store) Blob(ctx context.Context, sha string) (code.Blob, error) {
	id, ok := code.CanonicalSHA256(sha)
	if !ok {
		return code.Blob{}, fmt.Errorf("%w: blob %q", code.ErrNotFound, sha)
	}
	var d blobDoc
	err := s.blobs.FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return code.Blob{}, fmt.Errorf("%w: blob %s", code.ErrNotFound, sha)
	}
	if err != nil {
		return code.Blob{}, err
	}
	return code.Blob{SHA256: d.SHA256, Size: d.Size, Content: d.Content}, nil
}

// Usage returns the number of blobs and their total size.
func (s *Store) Usage(ctx context.Context) (int64, int64, error) {
	cur, err := s.blobs.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.M{"_id": nil, "blobs": bson.M{"$sum": 1}, "bytes": bson.M{"$sum": "$size"}}}},
	})
	if err != nil {
		return 0, 0, err
	}
	var rows []struct {
		Blobs int64 `bson:"blobs"`
		Bytes int64 `bson:"bytes"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return 0, 0, err
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	return rows[0].Blobs, rows[0].Bytes, nil
}

// Worktrees.

// PutWorktree creates or updates the worktree's metadata, adds w.Refs and
// clears Removed.
func (s *Store) PutWorktree(ctx context.Context, w code.Worktree) error {
	id, err := canonicalWorktree(w.ID)
	if err != nil {
		return err
	}
	refs := []string{}
	for _, r := range w.Refs {
		if !code.ValidRef(r) {
			return invalid("ref %q", r)
		}
		refs = append(refs, r)
	}
	_, err = s.worktrees.UpdateOne(ctx, bson.M{"_id": id}, bson.M{
		"$set": bson.M{
			"owner": w.Owner, "source": w.Source, "root": w.Root, "host": w.Host,
			"origin": w.Origin, "default_branch": w.DefaultBranch,
			"removed": false, "updated_at": time.Now().UTC(),
		},
		"$addToSet":    bson.M{"refs": bson.M{"$each": refs}},
		"$setOnInsert": bson.M{"sessions": bson.A{}},
	}, options.UpdateOne().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) { // a concurrent first put; this one updates
		return s.PutWorktree(ctx, w)
	}
	return err
}

// touchPipeline is the update that adds refs to a worktree, records the
// activity of sessionID (when not empty) and moves updated_at, in one
// atomic write. Every caller value is wrapped in $literal so a ref or
// session id starting with "$" is never read as a field path.
func touchPipeline(now time.Time, refs []string, sessionID string, ended bool) mongo.Pipeline {
	set := bson.D{{Key: "updated_at", Value: now}}
	if len(refs) > 0 {
		set = append(set, bson.E{Key: "refs", Value: bson.M{"$setUnion": bson.A{
			bson.M{"$ifNull": bson.A{"$refs", bson.A{}}}, bson.M{"$literal": refs},
		}}})
	}
	if sessionID != "" {
		sid := bson.M{"$literal": sessionID}
		set = append(set, bson.E{Key: "sessions", Value: bson.M{"$let": bson.M{
			"vars": bson.M{"ss": bson.M{"$ifNull": bson.A{"$sessions", bson.A{}}}},
			"in": bson.M{"$cond": bson.A{
				bson.M{"$in": bson.A{sid, "$$ss.id"}},
				bson.M{"$map": bson.M{"input": "$$ss", "as": "s", "in": bson.M{"$cond": bson.A{
					bson.M{"$eq": bson.A{"$$s.id", sid}},
					bson.M{"$mergeObjects": bson.A{"$$s", bson.M{
						"last_at": now,
						"ended":   bson.M{"$or": bson.A{"$$s.ended", ended}},
					}}},
					"$$s",
				}}}},
				bson.M{"$concatArrays": bson.A{"$$ss", bson.A{bson.D{
					{Key: "id", Value: sid}, {Key: "first_at", Value: now},
					{Key: "last_at", Value: now}, {Key: "ended", Value: ended},
				}}}},
			}},
		}}})
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

// touch applies touchPipeline to the worktree, code.ErrNotFound when there
// is none.
func (s *Store) touch(ctx context.Context, worktree string, refs []string, sessionID string, ended bool) error {
	id, err := canonicalWorktree(worktree)
	if err != nil {
		return err
	}
	res, err := s.worktrees.UpdateOne(ctx, bson.M{"_id": id}, touchPipeline(time.Now().UTC(), refs, sessionID, ended))
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%w: worktree %s", code.ErrNotFound, id)
	}
	return nil
}

// TouchSession records the activity of sessionID on the worktree.
func (s *Store) TouchSession(ctx context.Context, worktree, sessionID string, ended bool) error {
	if !code.ValidWorktreeID(worktree) {
		return invalid("worktree id %q", worktree)
	}
	if sessionID == "" {
		return invalid("empty session id")
	}
	return s.touch(ctx, worktree, nil, sessionID, ended)
}

// Worktrees lists every worktree, removed ones included, by id.
func (s *Store) Worktrees(ctx context.Context) ([]code.Worktree, error) {
	cur, err := s.worktrees.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []worktreeDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]code.Worktree, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.worktree())
	}
	return out, nil
}

func (s *Store) worktreeExists(ctx context.Context, worktree string) (string, error) {
	id, err := canonicalWorktree(worktree)
	if err != nil {
		return "", err
	}
	var d struct {
		Owner string `bson:"owner"`
	}
	err = s.worktrees.FindOne(ctx, bson.M{"_id": id}, options.FindOne().SetProjection(bson.M{"owner": 1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", fmt.Errorf("%w: worktree %s", code.ErrNotFound, id)
	}
	return d.Owner, err
}

// Commits.

// atomically runs fn in a transaction when the deployment supports one, and
// directly otherwise; fn must then order its writes so a retry is
// idempotent.
func (s *Store) atomically(ctx context.Context, fn func(ctx context.Context) (int64, error)) (int64, error) {
	if !s.tx {
		return fn(ctx)
	}
	sess, err := s.client.StartSession()
	if err != nil {
		return 0, err
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	out, err := sess.WithTransaction(ctx, func(ctx context.Context) (any, error) { return fn(ctx) })
	if err != nil {
		return 0, err
	}
	return out.(int64), nil
}

// nextSeq takes the next number of the project's change sequence.
func (s *Store) nextSeq(ctx context.Context) (int64, error) {
	return s.reserve(ctx, changesCounter, 1)
}

// reserve takes the next n numbers of the counter's sequence and returns the
// last of them.
func (s *Store) reserve(ctx context.Context, counter string, n int64) (int64, error) {
	var d struct {
		Seq int64 `bson:"seq"`
	}
	err := s.counters.FindOneAndUpdate(ctx, bson.M{"_id": counter}, bson.M{"$inc": bson.M{"seq": n}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&d)
	if err != nil {
		return 0, fmt.Errorf("mongocode: next %s seq: %w", counter, err)
	}
	return d.Seq, nil
}

// appendChange numbers d and inserts it into the log.
func (s *Store) appendChange(ctx context.Context, d changeDoc) (int64, error) {
	seq, err := s.nextSeq(ctx)
	if err != nil {
		return 0, err
	}
	d.Seq = seq
	if d.Upserts == nil {
		d.Upserts = []entryDoc{}
	}
	if d.Deletes == nil {
		d.Deletes = []string{}
	}
	if _, err := s.changes.InsertOne(ctx, d); err != nil {
		return 0, err
	}
	return seq, nil
}

// Commit applies c to its tree and appends it to the log atomically.
func (s *Store) Commit(ctx context.Context, c code.Change) (int64, error) {
	if err := validateChange(c); err != nil {
		return 0, err
	}
	var shas []string
	seen := map[string]bool{}
	upserts := make([]entryDoc, 0, len(c.Upserts))
	for _, e := range c.Upserts {
		if e.SHA256 != "" && !seen[e.SHA256] {
			seen[e.SHA256] = true
			shas = append(shas, e.SHA256)
		}
		upserts = append(upserts, toEntryDoc(e))
	}
	deletes := append([]string{}, c.Deletes...)

	return s.atomically(ctx, func(ctx context.Context) (int64, error) {
		if _, err := s.worktreeExists(ctx, c.Worktree); err != nil {
			return 0, err
		}
		missing, err := s.missing(ctx, shas)
		if err != nil {
			return 0, err
		}
		if len(missing) > 0 {
			return 0, fmt.Errorf("%w: %d blob(s), first %s", code.ErrMissingBlob, len(missing), missing[0])
		}
		now := time.Now().UTC()
		// 1. The tree: absolute writes, so applying them twice is applying
		// them once.
		if err := s.applyTree(ctx, c, upserts); err != nil {
			return 0, err
		}
		// 2. The worktree: refs and sessions are sets.
		if err := s.touch(ctx, c.Worktree, []string{c.Ref}, c.SessionID, false); err != nil {
			return 0, err
		}
		// 3. The log, last: a retry after a failure here appends the change
		// once more, which replays to the same tree.
		return s.appendChange(ctx, changeDoc{
			Owner: c.Owner, Worktree: c.Worktree, Ref: c.Ref, Head: c.Head, Upstream: c.Upstream,
			Ahead: c.Ahead, SessionID: c.SessionID, Upserts: upserts, Deletes: deletes, Full: c.Full, At: now,
		})
	})
}

// applyTree writes c's deletes and upserts (or, with Full, makes the tree
// exactly its upserts) to the (worktree, ref) tree. Paths and the ref reach
// the database only through treeKey and entryID: a delete removes the
// entries at their entryIDs, and a full reconcile reads the tree's _ids and
// deletes those it does not keep.
func (s *Store) applyTree(ctx context.Context, c code.Change, upserts []entryDoc) error {
	if c.Full {
		keep := make(map[string]bool, len(upserts))
		for _, e := range upserts {
			keep[entryID(c.Worktree, c.Ref, e.Path)] = true
		}
		cur, err := s.trees.Find(ctx, bson.M{"tree": treeKey(c.Worktree, c.Ref)}, options.Find().SetProjection(bson.M{"_id": 1}))
		if err != nil {
			return err
		}
		var held []struct {
			ID string `bson:"_id"`
		}
		if err := cur.All(ctx, &held); err != nil {
			return err
		}
		var stale []string
		for _, h := range held {
			if !keep[h.ID] {
				stale = append(stale, h.ID)
			}
		}
		if err := s.deleteIDs(ctx, stale); err != nil {
			return err
		}
	} else if len(c.Deletes) > 0 {
		ids := make([]string, 0, len(c.Deletes))
		for _, p := range c.Deletes {
			ids = append(ids, entryID(c.Worktree, c.Ref, p))
		}
		if err := s.deleteIDs(ctx, ids); err != nil {
			return err
		}
	}
	if len(upserts) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(upserts))
	for _, e := range upserts {
		d := toTreeDoc(c.Worktree, c.Ref, e)
		models = append(models, mongo.NewReplaceOneModel().
			SetFilter(bson.M{"_id": d.ID}).
			SetReplacement(d).
			SetUpsert(true))
	}
	_, err := s.trees.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(true))
	return err
}

// RemoveWorktree marks the worktree removed, drops its trees and appends a
// tombstone, in that order when there is no transaction.
func (s *Store) RemoveWorktree(ctx context.Context, worktree string) error {
	id, err := canonicalWorktree(worktree)
	if err != nil {
		return err
	}
	_, err = s.atomically(ctx, func(ctx context.Context) (int64, error) {
		var d struct {
			Owner string `bson:"owner"`
		}
		now := time.Now().UTC()
		err := s.worktrees.FindOneAndUpdate(ctx, bson.M{"_id": id},
			bson.M{"$set": bson.M{"removed": true, "updated_at": now}},
			options.FindOneAndUpdate().SetProjection(bson.M{"owner": 1})).Decode(&d)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, fmt.Errorf("%w: worktree %s", code.ErrNotFound, id)
		}
		if err != nil {
			return 0, err
		}
		if _, err := s.trees.DeleteMany(ctx, bson.M{"worktree": id}); err != nil {
			return 0, err
		}
		return s.appendChange(ctx, changeDoc{Owner: d.Owner, Worktree: id, Full: true, At: now})
	})
	return err
}

// RemoveRef drops the (worktree, ref) tree, removes ref from the worktree's
// refs and appends a ref tombstone, in that order when there is no
// transaction.
func (s *Store) RemoveRef(ctx context.Context, worktree, ref string) error {
	id, err := canonicalWorktree(worktree)
	if err != nil {
		return err
	}
	if !code.ValidRef(ref) {
		return invalid("ref %q", ref)
	}
	_, err = s.atomically(ctx, func(ctx context.Context) (int64, error) {
		if _, err := s.worktreeExists(ctx, id); err != nil {
			return 0, err
		}
		if _, err := s.trees.DeleteMany(ctx, bson.M{"tree": treeKey(id, ref)}); err != nil {
			return 0, err
		}
		now := time.Now().UTC()
		var d struct {
			Owner string `bson:"owner"`
		}
		// $pull matches ref as a value, so a ref starting with "$" is data.
		err := s.worktrees.FindOneAndUpdate(ctx, bson.M{"_id": id},
			bson.M{"$pull": bson.M{"refs": ref}, "$set": bson.M{"updated_at": now}},
			options.FindOneAndUpdate().SetProjection(bson.M{"owner": 1})).Decode(&d)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, fmt.Errorf("%w: worktree %s", code.ErrNotFound, id)
		}
		if err != nil {
			return 0, err
		}
		return s.appendChange(ctx, changeDoc{Owner: d.Owner, Worktree: id, Ref: ref, Full: true, At: now})
	})
	return err
}

// Events.

// AppendEvents validates every event, then reserves their seqs and inserts
// them in order.
func (s *Store) AppendEvents(ctx context.Context, events []code.Event) error {
	for _, e := range events {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	if len(events) == 0 {
		return nil
	}
	_, err := s.atomically(ctx, func(ctx context.Context) (int64, error) {
		last, err := s.reserve(ctx, eventsCounter, int64(len(events)))
		if err != nil {
			return 0, err
		}
		now := time.Now().UTC()
		docs := make([]any, 0, len(events))
		for i, e := range events {
			e.Seq = last - int64(len(events)) + 1 + int64(i)
			if e.At.IsZero() {
				e.At = now
			}
			docs = append(docs, toEventDoc(e))
		}
		_, err = s.events.InsertMany(ctx, docs, options.InsertMany().SetOrdered(true))
		return last, err
	})
	return err
}

// SHA256ForGitOID maps each git blob id held by a clean entry with content
// to that content's SHA-256, through the git_oid index of code_trees.
func (s *Store) SHA256ForGitOID(ctx context.Context, gitOIDs []string) (map[string]string, error) {
	oids := make([]string, 0, len(gitOIDs))
	for _, o := range gitOIDs {
		c, ok := code.CanonicalGitOID(o)
		if !ok {
			return nil, invalid("git oid %q", o)
		}
		oids = append(oids, c)
	}
	out := map[string]string{}
	if len(oids) == 0 {
		return out, nil
	}
	cur, err := s.trees.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"git_oid": bson.M{"$in": oids}, "dirty": false, "sha256": bson.M{"$ne": ""}}}},
		{{Key: "$group", Value: bson.M{"_id": "$git_oid", "sha256": bson.M{"$first": "$sha256"}}}},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		GitOID string `bson:"_id"`
		SHA256 string `bson:"sha256"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.GitOID] = r.SHA256
	}
	return out, nil
}

// Reads.

// Tree returns the entries of the (worktree, ref) tree sorted by path.
func (s *Store) Tree(ctx context.Context, worktree, ref string) ([]code.Entry, error) {
	cur, err := s.trees.Find(ctx, bson.M{"tree": treeKey(worktree, ref)}, options.Find().SetSort(bson.D{{Key: "path", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []treeDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]code.Entry, 0, len(docs))
	for _, d := range docs {
		if d.Worktree == worktree && d.Ref == ref { // belt and braces: the key encodes both
			out = append(out, d.entry())
		}
	}
	return out, nil
}

// Changes returns up to limit changes with Seq greater than afterSeq, in Seq
// order; a limit below 1 returns none.
func (s *Store) Changes(ctx context.Context, afterSeq int64, limit int) ([]code.Change, error) {
	if limit < 1 {
		return []code.Change{}, nil
	}
	cur, err := s.changes.Find(ctx, bson.M{"seq": bson.M{"$gt": afterSeq}},
		options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	var docs []changeDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]code.Change, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.change())
	}
	return out, nil
}

// Events returns up to limit events with Seq greater than afterSeq, in Seq
// order; a limit below 1 returns none.
func (s *Store) Events(ctx context.Context, afterSeq int64, limit int) ([]code.Event, error) {
	if limit < 1 {
		return []code.Event{}, nil
	}
	cur, err := s.events.Find(ctx, bson.M{"seq": bson.M{"$gt": afterSeq}},
		options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	var docs []eventDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]code.Event, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.event())
	}
	return out, nil
}
