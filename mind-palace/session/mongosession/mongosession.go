// Package mongosession is the MongoDB session.Store. Collection names, the
// index set and the session_full view match the clued Node daemon so both
// can share one database.
package mongosession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	sessions        = "sessions"
	hookEvents      = "hook_events"
	transcriptLines = "transcript_lines"
	subagentLines   = "subagent_lines"
	blobs           = "blobs"
	otelEvents      = "otel_events"
	sessionFull     = "session_full"

	maxSessions = 500

	// namespaceExists is the server error code for creating a collection that exists.
	namespaceExists = 48
)

// Store is a MongoDB-backed session.Store. Connect must be called before use.
type Store struct {
	// Logger receives index warnings; nil means slog.Default().
	Logger *slog.Logger

	url       string
	dbName    string
	retention int
	client    *mongo.Client
	db        *mongo.Database
}

var _ session.Store = (*Store)(nil)

// New prepares a store for cfg.MongoURL and cfg.DBName, keeping documents
// cfg.RetentionDays days when it is positive; the connection is made by
// Connect.
func New(cfg session.Config) *Store {
	return &Store{url: cfg.MongoURL, dbName: cfg.DBName, retention: cfg.RetentionDays}
}

// retentionIndex names the TTL index of each collection.
const retentionIndex = "retention_ttl"

// retentionFields are the times the TTL indexes expire documents by.
var retentionFields = []struct{ collection, field string }{
	{sessions, "last_seen"}, {hookEvents, "created_at"}, {transcriptLines, "created_at"},
	{subagentLines, "created_at"}, {blobs, "created_at"}, {otelEvents, "created_at"},
}

// applyRetention creates the TTL indexes for the retention days, changes
// their expiry when it moved, or drops them when retention is off.
func (s *Store) applyRetention(ctx context.Context) {
	for _, r := range retentionFields {
		ix := s.db.Collection(r.collection).Indexes()
		if s.retention <= 0 {
			_ = ix.DropOne(ctx, retentionIndex) // absent: nothing to drop
			continue
		}
		secs := int32(min(s.retention, 20000) * 86400)
		model := mongo.IndexModel{Keys: key(r.field, 1), Options: options.Index().SetName(retentionIndex).SetExpireAfterSeconds(secs)}
		if _, err := ix.CreateOne(ctx, model); err == nil {
			continue
		}
		err := s.db.RunCommand(ctx, key("collMod", r.collection, "index", key("name", retentionIndex, "expireAfterSeconds", secs))).Err()
		if err != nil {
			s.logger().Warn("clued: retention index", "collection", r.collection, "error", err)
		}
	}
}

func (s *Store) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

type index struct {
	collection string
	keys       bson.D
	unique     bool
}

func key(fields ...any) bson.D {
	d := make(bson.D, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		d = append(d, bson.E{Key: fields[i].(string), Value: fields[i+1]})
	}
	return d
}

var indexes = []index{
	{sessions, key("session_id", 1), true},
	{sessions, key("git_origin", 1), false},
	{hookEvents, key("session_id", 1), false},
	{hookEvents, key("created_at", -1), false},
	{transcriptLines, key("session_id", 1, "seq", 1), true},
	{sessions, key("account_id", 1, "last_seen", -1), false},
	{sessions, key("account_id", 1, "git_origin", 1), false},
	{sessions, key("account_id", 1, "git_origin", 1, "git_branch", 1), false},
	{hookEvents, key("account_id", 1, "session_id", 1, "created_at", -1), false},
	{transcriptLines, key("account_id", 1, "session_id", 1, "seq", 1), false},
	{subagentLines, key("session_id", 1, "subagent_id", 1, "seq", 1), true},
	{subagentLines, key("account_id", 1, "session_id", 1, "subagent_id", 1, "seq", 1), false},
	{blobs, key("session_id", 1, "blob_type", 1, "name", 1), true},
	{blobs, key("account_id", 1, "session_id", 1, "blob_type", 1), false},
	{hookEvents, key("account_id", 1, "enrichments.file-tracker.file_path", 1), false},
	{hookEvents, key("account_id", 1, "memory.project", 1, "memory.kind", 1, "created_at", 1), false},
	{hookEvents, key("account_id", 1, "memory.id", 1), false},
	{transcriptLines, key("account_id", 1, "enrichments.error-flag.is_error", 1, "created_at", -1), false},
	{transcriptLines, key("account_id", 1, "enrichments.usage.day", 1), false},
	{otelEvents, key("account_id", 1, "session_id", 1, "name", 1, "sequence", 1, "timestamp", 1), true},
	{otelEvents, key("account_id", 1, "session_id", 1, "timestamp", -1), false},
	{otelEvents, key("account_id", 1, "timestamp", 1), false},
}

// partialIndexes are unique indexes limited to the documents matching a
// filter: one MemorySaved event per account and record hash.
var partialIndexes = []struct {
	collection    string
	keys, partial bson.D
}{
	{hookEvents, key("account_id", 1, "memory.hash", 1), key("memory.hash", key("$type", "string"))},
}

// joinLookup joins a sub-collection onto a session by session_id.
func joinLookup(from string, sort bson.D, unset ...string) bson.D {
	return key("$lookup", key(
		"from", from,
		"let", key("sid", "$session_id"),
		"pipeline", bson.A{
			key("$match", key("$expr", key("$eq", bson.A{"$session_id", "$$sid"}))),
			key("$sort", sort),
			key("$unset", unset),
		},
		"as", from,
	))
}

// Joins every sub-collection of a session into one document. Blob content is
// excluded to avoid multi-MB payloads; session_id / account_id / host are
// stripped from the sub-arrays because the parent already carries them.
var sessionFullPipeline = bson.A{
	joinLookup(transcriptLines, key("seq", 1), "_id", "session_id", "account_id", "host"),
	joinLookup(subagentLines, key("subagent_id", 1, "seq", 1), "_id", "session_id", "account_id", "host"),
	joinLookup(blobs, key("blob_type", 1, "name", 1), "_id", "session_id", "account_id", "content"),
	joinLookup(hookEvents, key("created_at", 1), "_id", "session_id", "account_id"),
}

// Connect connects, pings, creates the indexes and creates or updates the
// session_full view. Index failures (for example an older non-unique index)
// are logged as warnings because queries still work. Calling it again
// re-applies the indexes and the view pipeline.
func (s *Store) Connect(ctx context.Context) error {
	if s.client == nil {
		client, err := mongo.Connect(options.Client().ApplyURI(s.url))
		if err != nil {
			return err
		}
		if err := client.Ping(ctx, nil); err != nil {
			_ = client.Disconnect(ctx)
			return err
		}
		s.client, s.db = client, client.Database(s.dbName)
	}

	for _, ix := range indexes {
		model := mongo.IndexModel{Keys: ix.keys}
		if ix.unique {
			model.Options = options.Index().SetUnique(true)
		}
		if _, err := s.db.Collection(ix.collection).Indexes().CreateOne(ctx, model); err != nil {
			s.logger().Warn("clued: index warning", "collection", ix.collection, "keys", fmt.Sprint(ix.keys), "error", err)
		}
	}
	// The one text index of transcript_lines; its account_id prefix
	// scopes every search to an account, which $text then requires.
	textIndex := mongo.IndexModel{Keys: key("account_id", 1, "enrichments.search-text", "text"),
		Options: options.Index().SetName("transcript_text").SetDefaultLanguage("none")}
	if _, err := s.db.Collection(transcriptLines).Indexes().CreateOne(ctx, textIndex); err != nil {
		s.logger().Warn("clued: index warning", "collection", transcriptLines, "keys", "text", "error", err)
	}
	for _, ix := range partialIndexes {
		model := mongo.IndexModel{Keys: ix.keys, Options: options.Index().SetUnique(true).SetPartialFilterExpression(ix.partial)}
		if _, err := s.db.Collection(ix.collection).Indexes().CreateOne(ctx, model); err != nil {
			s.logger().Warn("clued: index warning", "collection", ix.collection, "keys", fmt.Sprint(ix.keys), "error", err)
		}
	}

	s.applyRetention(ctx)

	err := s.db.CreateView(ctx, sessionFull, sessions, sessionFullPipeline)
	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) && cmdErr.Code == namespaceExists {
		err = s.db.RunCommand(ctx, key("collMod", sessionFull, "viewOn", sessions, "pipeline", sessionFullPipeline)).Err()
	}
	return err
}

// Close disconnects from MongoDB.
func (s *Store) Close(ctx context.Context) error {
	if s.client == nil {
		return nil
	}
	client := s.client
	s.client, s.db = nil, nil
	return client.Disconnect(ctx)
}

// --- shaping ---

// normalize converts driver types to plain Go: documents to map[string]any,
// arrays to []any and datetimes to time.Time. ObjectIDs are left as they are.
func normalize(v any) any {
	switch x := v.(type) {
	case bson.D:
		return normalizeDoc(x)
	case bson.M:
		return normalizeMap(x)
	case map[string]any:
		return normalizeMap(x)
	case bson.A:
		return normalizeList(x)
	case []any:
		return normalizeList(x)
	case bson.DateTime:
		return x.Time().UTC()
	}
	return v
}

func normalizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, e := range m {
		out[k] = normalize(e)
	}
	return out
}

func normalizeDoc(d bson.D) map[string]any {
	out := make(map[string]any, len(d))
	for _, e := range d {
		out[e.Key] = normalize(e.Value)
	}
	return out
}

func normalizeList(l []any) []any {
	out := make([]any, len(l))
	for i, e := range l {
		out[i] = normalize(e)
	}
	return out
}

func toDocs(raw []bson.D) []session.Doc {
	out := make([]session.Doc, len(raw))
	for i, d := range raw {
		out[i] = normalizeDoc(d)
	}
	return out
}

// regexFilter matches case-insensitively; the pattern is validated first so
// a bad one is rejected here rather than by the server.
func regexFilter(pattern string) (bson.D, error) {
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, err
	}
	return key("$regex", pattern, "$options", "i"), nil
}

// objectID accepts an ObjectID or its hex form; anything else is used as is.
func objectID(id any) any {
	if hex, ok := id.(string); ok {
		if oid, err := bson.ObjectIDFromHex(hex); err == nil {
			return oid
		}
	}
	return id
}

// --- queries ---

func (s *Store) coll(name string) *mongo.Collection { return s.db.Collection(name) }

// DeleteAccount deletes the account's documents from every collection the
// store writes (session_full is a view over them).
func (s *Store) DeleteAccount(ctx context.Context, accountID string) (int64, error) {
	if accountID == "" {
		return 0, session.ErrNoAccount
	}
	var n int64
	for _, name := range []string{hookEvents, transcriptLines, subagentLines, blobs, otelEvents, sessions} {
		res, err := s.coll(name).DeleteMany(ctx, bson.M{"account_id": accountID})
		if err != nil {
			return n, fmt.Errorf("delete account from %s: %w", name, err)
		}
		n += res.DeletedCount
	}
	return n, nil
}

// DeleteSessions deletes the sessions the query selects with their
// documents, oldest last_seen first under a limit, and orphaned captures
// when asked.
func (s *Store) DeleteSessions(ctx context.Context, q session.ForgetQuery) (session.Forgotten, error) {
	out := session.Forgotten{Sessions: []string{}}
	if q.AccountID == "" {
		return out, session.ErrNoAccount
	}
	if q.Empty() {
		return out, nil
	}
	ids := q.SessionIDs
	if q.ProjectPathRe != "" || !q.Before.IsZero() {
		filter := key("account_id", q.AccountID)
		if len(q.SessionIDs) > 0 {
			filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
		}
		if q.ProjectPathRe != "" {
			re, err := regexFilter(q.ProjectPathRe)
			if err != nil {
				return out, err
			}
			filter = append(filter, bson.E{Key: "project_path", Value: re})
		}
		if !q.Before.IsZero() {
			filter = append(filter, bson.E{Key: "last_seen", Value: key("$lt", q.Before)})
		}
		limit := 0
		if q.Limit > 0 {
			limit = q.Limit + 1
		}
		docs, err := s.find(ctx, sessions, filter, key("_id", 0, "session_id", 1), key("last_seen", 1, "_id", 1), 0, limit)
		if err != nil {
			return out, err
		}
		if q.Limit > 0 && len(docs) > q.Limit {
			docs, out.More = docs[:q.Limit], true
		}
		ids = make([]string, 0, len(docs))
		for _, d := range docs {
			if sid, _ := d.String("session_id"); sid != "" {
				ids = append(ids, sid)
			}
		}
		if q.Orphans && q.ProjectPathRe == "" {
			orphans, more, err := s.orphans(ctx, q)
			if err != nil {
				return out, err
			}
			ids = append(ids, orphans...)
			out.More = out.More || more
		}
	}
	ids = uniqueSorted(ids)
	if len(ids) == 0 {
		return out, nil
	}
	for _, name := range []string{hookEvents, transcriptLines, subagentLines, blobs, otelEvents, sessions} {
		res, err := s.coll(name).DeleteMany(ctx, key("account_id", q.AccountID, "session_id", key("$in", ids)))
		if err != nil {
			return out, fmt.Errorf("forget sessions in %s: %w", name, err)
		}
		out.Documents += res.DeletedCount
	}
	out.Sessions = ids
	return out, nil
}

// orphans returns, per capture collection, up to q.Limit session ids (all
// with no limit) of the account's documents created before q.Before
// (timestamp for OpenTelemetry events) whose session has no record in the
// account, oldest first; only of q.SessionIDs when given. It reports
// whether a collection held more.
func (s *Store) orphans(ctx context.Context, q session.ForgetQuery) ([]string, bool, error) {
	var ids []string
	more := false
	for _, col := range []string{hookEvents, transcriptLines, subagentLines, blobs, otelEvents} {
		field := "created_at"
		if col == otelEvents {
			field = "timestamp"
		}
		sid := key("$type", "string", "$ne", "")
		if len(q.SessionIDs) > 0 {
			sid = append(sid, bson.E{Key: "$in", Value: q.SessionIDs})
		}
		pipeline := bson.A{
			key("$match", key("account_id", q.AccountID, field, key("$lt", q.Before), "session_id", sid)),
			key("$group", key("_id", "$session_id", "first", key("$min", "$"+field))),
			key("$lookup", key("from", sessions, "let", key("sid", "$_id"), "as", "record", "pipeline", bson.A{
				key("$match", key("account_id", q.AccountID, "$expr", key("$eq", bson.A{"$session_id", "$$sid"}))),
				key("$limit", 1),
				key("$project", key("_id", 1)),
			})),
			key("$match", key("record", key("$size", 0))),
			key("$sort", key("first", 1, "_id", 1)),
		}
		if q.Limit > 0 {
			pipeline = append(pipeline, key("$limit", q.Limit+1))
		}
		pipeline = append(pipeline, key("$project", key("_id", 1)))
		cur, err := s.coll(col).Aggregate(ctx, pipeline)
		if err != nil {
			return nil, false, fmt.Errorf("find orphans in %s: %w", col, err)
		}
		var rows []struct {
			ID string `bson:"_id"`
		}
		if err := cur.All(ctx, &rows); err != nil {
			return nil, false, fmt.Errorf("find orphans in %s: %w", col, err)
		}
		if q.Limit > 0 && len(rows) > q.Limit {
			rows, more = rows[:q.Limit], true
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
	}
	return ids, more, nil
}

// uniqueSorted sorts the ids and drops repeats.
func uniqueSorted(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	n := 0
	for i, id := range out {
		if i == 0 || id != out[n-1] {
			out[n] = id
			n++
		}
	}
	return out[:n]
}

// find runs a query and returns the documents; a limit of zero or less means no limit.
func (s *Store) find(ctx context.Context, col string, filter bson.D, projection, sort bson.D, skip, limit int) ([]session.Doc, error) {
	opts := options.Find()
	if projection != nil {
		opts.SetProjection(projection)
	}
	if sort != nil {
		opts.SetSort(sort)
	}
	if skip > 0 {
		opts.SetSkip(int64(skip))
	}
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := s.coll(col).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var raw []bson.D
	if err := cur.All(ctx, &raw); err != nil {
		return nil, err
	}
	return toDocs(raw), nil
}

func (s *Store) upsert(ctx context.Context, col string, filter, set, onInsert bson.D) error {
	update := key("$set", set)
	if onInsert != nil {
		update = append(update, bson.E{Key: "$setOnInsert", Value: onInsert})
	}
	_, err := s.coll(col).UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}

func (s *Store) update(ctx context.Context, col string, filter, set bson.D) error {
	_, err := s.coll(col).UpdateOne(ctx, filter, key("$set", set))
	return err
}

// setPresent appends the string fields whose value is not empty.
func setPresent(d bson.D, fields ...string) bson.D {
	for i := 0; i < len(fields); i += 2 {
		if fields[i+1] != "" {
			d = append(d, bson.E{Key: fields[i], Value: fields[i+1]})
		}
	}
	return d
}

// withRedacted records the redaction rules that fired.
func withRedacted(d bson.D, rules []string) bson.D {
	if len(rules) > 0 {
		d = append(d, bson.E{Key: "redacted", Value: rules})
	}
	return d
}

func withHost(d bson.D, h *session.HostInfo) bson.D {
	if h != nil {
		d = append(d, bson.E{Key: "host", Value: h})
	}
	return d
}

// --- sessions ---

func (s *Store) UpsertSession(ctx context.Context, in session.Session) error {
	set := setPresent(key("session_id", in.SessionID, "last_seen", in.LastSeen),
		"transcript_path", in.TranscriptPath, "cwd", in.Cwd, "project_path", in.ProjectPath,
		"git_origin", in.GitOrigin, "git_branch", in.GitBranch, "account_id", in.AccountID)
	return s.upsert(ctx, sessions, key("session_id", in.SessionID), withHost(set, in.Host), key("started_at", in.LastSeen))
}

func (s *Store) TouchSession(ctx context.Context, accountID, sessionID string, at time.Time) error {
	return s.update(ctx, sessions, key("session_id", sessionID, "account_id", accountID), key("last_seen", at))
}

func (s *Store) SetGitInfoIfMissing(ctx context.Context, sessionID, origin, branch string) error {
	set := setPresent(key("git_origin", origin), "git_branch", branch)
	return s.update(ctx, sessions, key("session_id", sessionID, "git_origin", key("$exists", false)), set)
}

func (s *Store) SetModel(ctx context.Context, accountID, sessionID, model string, at time.Time) (string, bool, error) {
	if model == "" {
		return "", false, nil
	}
	filter := key("session_id", sessionID, "account_id", accountID, "model", key("$ne", model))
	update := key("$set", key("model", model, "model_changed_at", at))
	var before bson.D
	err := s.coll(sessions).FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetProjection(key("_id", 0, "model", 1))).Decode(&before)
	if errors.Is(err, mongo.ErrNoDocuments) {
		// Either the session already runs on this model or it does not exist.
		if _, gerr := s.GetSession(ctx, accountID, sessionID); gerr != nil {
			return "", false, gerr
		}
		return model, false, nil
	}
	if err != nil {
		return "", false, err
	}
	previous, _ := session.Doc(normalizeDoc(before)).String("model")
	return previous, true, nil
}

func (s *Store) SetSessionMeta(ctx context.Context, accountID, sessionID string, fields session.Doc) error {
	if len(fields) == 0 {
		return nil
	}
	set := bson.D{}
	for k, v := range fields {
		set = append(set, bson.E{Key: k, Value: v})
	}
	res, err := s.coll(sessions).UpdateOne(ctx, key("session_id", sessionID, "account_id", accountID), key("$set", set))
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return session.ErrNotFound
	}
	return nil
}

func (s *Store) SetLiveState(ctx context.Context, accountID, sessionID, state, reason string, at time.Time) (string, bool, error) {
	if state == "" {
		return "", false, nil
	}
	filter := key("session_id", sessionID, "account_id", accountID, "live_state", key("$ne", state))
	update := key("$set", key("live_state", state, "live_state_at", at, "live_state_reason", reason))
	var before bson.D
	err := s.coll(sessions).FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetProjection(key("_id", 0, "live_state", 1))).Decode(&before)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if _, gerr := s.GetSession(ctx, accountID, sessionID); gerr != nil {
			return "", false, gerr
		}
		return state, false, nil
	}
	if err != nil {
		return "", false, err
	}
	previous, _ := session.Doc(normalizeDoc(before)).String("live_state")
	return previous, true, nil
}

func (s *Store) StateChanges(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	filter := key("hook_event_name", session.StateChangedEventName, "account_id", q.AccountID)
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "created_at", Value: key("$gte", q.Since)})
	}
	projection := key("_id", 0, "session_id", 1, "from", 1, "to", 1, "reason", 1, "at", 1, "created_at", 1)
	return s.find(ctx, hookEvents, filter, projection, key("created_at", -1), 0, q.Limit)
}

func (s *Store) RejectedToolUses(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	filter := key("account_id", q.AccountID, "enrichments.error-flag.rejected", true)
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "created_at", Value: key("$gte", q.Since)})
	}
	projection := key("_id", 0, "session_id", 1, "seq", 1, "enrichments.error-flag", 1, "created_at", 1)
	return s.find(ctx, transcriptLines, filter, projection, key("created_at", -1, "seq", -1), 0, q.Limit)
}

func (s *Store) LastModelBefore(ctx context.Context, sessionID string, seq int, at time.Time) (string, error) {
	filter := key("session_id", sessionID, "line.type", "assistant",
		"line.message.model", key("$exists", true, "$nin", bson.A{"", session.SyntheticModel}))
	if seq >= 0 {
		filter = append(filter, bson.E{Key: "seq", Value: key("$lt", seq)})
	}
	if !at.IsZero() {
		filter = append(filter, bson.E{Key: "created_at", Value: key("$lte", at)})
	}
	docs, err := s.find(ctx, transcriptLines, filter, key("_id", 0, "line.message.model", 1), key("seq", -1), 0, 1)
	if err != nil || len(docs) == 0 {
		return "", err
	}
	model, _ := docs[0].Message().String("model")
	return model, nil
}

func (s *Store) GetSession(ctx context.Context, accountID, sessionID string) (session.Doc, error) {
	return s.findOne(ctx, sessions, key("session_id", sessionID, "account_id", accountID))
}

// FullSession assembles the session_full document from one query per
// collection. It does not read the session_full view, whose result is a single
// document and so fails past the 16 MB document limit on long sessions.
func (s *Store) FullSession(ctx context.Context, accountID, sessionID string) (session.Doc, error) {
	full, err := s.findOne(ctx, sessions, key("session_id", sessionID, "account_id", accountID))
	if err != nil {
		return nil, err
	}
	for _, part := range []struct {
		collection string
		sort       bson.D
		stripped   []string
	}{
		{transcriptLines, key("seq", 1), []string{"host"}},
		{subagentLines, key("subagent_id", 1, "seq", 1), []string{"host"}},
		{blobs, key("blob_type", 1, "name", 1), []string{"content"}},
		{hookEvents, key("created_at", 1), nil},
	} {
		projection := key("_id", 0, "session_id", 0, "account_id", 0)
		for _, field := range part.stripped {
			projection = append(projection, bson.E{Key: field, Value: 0})
		}
		docs, err := s.find(ctx, part.collection, key("session_id", sessionID), projection, part.sort, 0, 0)
		if err != nil {
			return nil, err
		}
		list := make([]any, len(docs))
		for i, d := range docs {
			list[i] = map[string]any(d)
		}
		full[part.collection] = list
	}
	return full, nil
}

func (s *Store) findOne(ctx context.Context, col string, filter bson.D) (session.Doc, error) {
	var raw bson.D
	err := s.coll(col).FindOne(ctx, filter, options.FindOne().SetProjection(key("_id", 0))).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return normalizeDoc(raw), nil
}

func (s *Store) FindSessions(ctx context.Context, q session.SessionQuery) ([]session.Doc, error) {
	filter := key("account_id", q.AccountID)
	for _, f := range []struct{ field, pattern string }{{"project_path", q.ProjectPathRe}, {"git_origin", q.GitOriginRe}, {"title", q.TitleRe}} {
		if f.pattern == "" {
			continue
		}
		re, err := regexFilter(f.pattern)
		if err != nil {
			return nil, err
		}
		filter = append(filter, bson.E{Key: f.field, Value: re})
	}
	if q.QueryRe != "" {
		re, err := regexFilter(q.QueryRe)
		if err != nil {
			return nil, err
		}
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{key("project_path", re), key("cwd", re)}})
	}
	if len(q.LiveStates) > 0 {
		filter = append(filter, bson.E{Key: "live_state", Value: key("$in", q.LiveStates)})
	}
	if !q.LastSeenFrom.IsZero() || !q.LastSeenBefore.IsZero() {
		bounds := bson.D{}
		if !q.LastSeenFrom.IsZero() {
			bounds = append(bounds, bson.E{Key: "$gte", Value: q.LastSeenFrom})
		}
		if !q.LastSeenBefore.IsZero() {
			bounds = append(bounds, bson.E{Key: "$lt", Value: q.LastSeenBefore})
		}
		filter = append(filter, bson.E{Key: "last_seen", Value: bounds})
	}
	limit := q.Limit
	if limit <= 0 || limit > maxSessions {
		limit = maxSessions
	}
	projection := key("_id", 0, "session_id", 1, "project_path", 1, "git_origin", 1, "cwd", 1, "model", 1, "live_state", 1, "live_state_at", 1, "live_state_reason", 1,
		"title", 1, "title_kind", 1, "slug", 1, "permission_mode", 1, "pr_link", 1, "worktree", 1, "entrypoint", 1, "claude_version", 1, "started_at", 1, "last_seen", 1)
	return s.find(ctx, sessions, filter, projection, key("last_seen", -1), 0, limit)
}

// --- hook events ---

func (s *Store) InsertHookEvent(ctx context.Context, ev session.Doc) error {
	_, err := s.coll(hookEvents).InsertOne(ctx, ev)
	if err != nil && mongo.IsDuplicateKeyError(err) && isMemoryEvent(ev) {
		return nil
	}
	return err
}

// isMemoryEvent reports whether ev is a MemorySaved event with a record hash.
func isMemoryEvent(ev session.Doc) bool {
	name, _ := ev.String("hook_event_name")
	hash, _ := ev.Map("memory").String("hash")
	return name == session.MemoryEventName && hash != ""
}

func (s *Store) MemoryEvents(ctx context.Context, q session.MemoryQuery) ([]session.Doc, error) {
	text := key("$type", "string")
	if q.PatternRe != "" {
		re, err := regexFilter(q.PatternRe)
		if err != nil {
			return nil, err
		}
		text = re
	}
	filter := key("hook_event_name", session.MemoryEventName, "memory.text", text, "account_id", q.AccountID)
	if len(q.Kinds) > 0 {
		filter = append(filter, bson.E{Key: "memory.kind", Value: key("$in", q.Kinds)})
	}
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	if len(q.Projects) > 0 {
		filter = append(filter, bson.E{Key: "memory.project", Value: key("$in", q.Projects)})
	}
	if len(q.IDs) > 0 {
		filter = append(filter, bson.E{Key: "memory.id", Value: key("$in", q.IDs)})
	}
	if !q.IncludePrivate {
		filter = append(filter, bson.E{Key: "memory.private", Value: key("$ne", true)})
	}
	created := bson.D{}
	if !q.Since.IsZero() {
		created = append(created, bson.E{Key: "$gte", Value: q.Since})
	}
	if !q.Until.IsZero() {
		created = append(created, bson.E{Key: "$lt", Value: q.Until})
	}
	if len(created) > 0 {
		filter = append(filter, bson.E{Key: "created_at", Value: created})
	}
	projection := key("_id", 0, "session_id", 1, "cwd", 1, "memory", 1, "created_at", 1)
	return s.find(ctx, hookEvents, filter, projection, key("created_at", -1), 0, q.Limit)
}

func (s *Store) DeleteMemory(ctx context.Context, q session.MemoryDelete) (int, error) {
	if q.AccountID == "" {
		return 0, session.ErrNoAccount
	}
	if q.Empty() {
		return 0, nil
	}
	var or bson.A
	if len(q.IDs) > 0 {
		or = append(or, key("memory.id", key("$in", q.IDs)))
	}
	if len(q.Hashes) > 0 {
		or = append(or, key("memory.hash", key("$in", q.Hashes)))
	}
	res, err := s.coll(hookEvents).DeleteMany(ctx, key("hook_event_name", session.MemoryEventName, "account_id", q.AccountID, "$or", or))
	if err != nil {
		return 0, err
	}
	return int(res.DeletedCount), nil
}

func (s *Store) LimitSamples(ctx context.Context, q session.LimitQuery) ([]session.Doc, error) {
	filter := key("hook_event_name", session.LimitsEventName, "account_id", q.AccountID)
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "created_at", Value: key("$gte", q.Since)})
	}
	projection := key("_id", 0, "session_id", 1, "cwd", 1, "model", 1, "version", 1, "context_window", 1, "rate_limits", 1, "cost", 1, "live_state", 1, "sampled_at", 1, "created_at", 1)
	return s.find(ctx, hookEvents, filter, projection, key("created_at", -1), 0, q.Limit)
}

// accountValue is the account to match: a missing account_id when empty.
func accountValue(accountID string) any {
	if accountID == "" {
		return nil
	}
	return accountID
}

// InsertOtelEvents upserts each event on its account, session, name,
// sequence and timestamp, writing only on insert, so a resent event changes
// nothing; the new ones are the upserts.
func (s *Store) InsertOtelEvents(ctx context.Context, events []session.OtelEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	now := time.Now()
	models := make([]mongo.WriteModel, len(events))
	for i, e := range events {
		at := e.Timestamp.UTC()
		filter := key("account_id", accountValue(e.AccountID), "session_id", e.SessionID, "name", e.Name, "sequence", e.Sequence, "timestamp", at)
		attrs := e.Attributes
		if attrs == nil {
			attrs = session.Doc{}
		}
		insert := withRedacted(setPresent(key("attributes", attrs, "created_at", now), "prompt_id", e.PromptID), e.Redacted)
		models[i] = mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(key("$setOnInsert", insert)).SetUpsert(true)
	}
	res, err := s.coll(otelEvents).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if res == nil {
		return 0, err
	}
	return int(res.UpsertedCount), err
}

func (s *Store) OtelEvents(ctx context.Context, q session.OtelQuery) ([]session.Doc, error) {
	filter := key("account_id", accountValue(q.AccountID))
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	if len(q.Names) > 0 {
		filter = append(filter, bson.E{Key: "name", Value: key("$in", q.Names)})
	}
	if q.PromptID != "" {
		filter = append(filter, bson.E{Key: "prompt_id", Value: q.PromptID})
	}
	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "timestamp", Value: key("$gte", q.Since)})
	}
	projection := key("_id", 0, "session_id", 1, "prompt_id", 1, "name", 1, "sequence", 1, "timestamp", 1, "attributes", 1, "redacted", 1, "created_at", 1)
	return s.find(ctx, otelEvents, filter, projection, key("timestamp", -1, "sequence", -1), 0, q.Limit)
}

func (s *Store) HookEventIDsByToolUse(ctx context.Context, sessionID string, toolUseIDs []string) ([]string, error) {
	ids := []string{}
	if len(toolUseIDs) == 0 {
		return ids, nil
	}
	filter := key("tool_use_id", key("$in", toolUseIDs))
	if sessionID != "" {
		filter = append(filter, bson.E{Key: "session_id", Value: sessionID})
	}
	docs, err := s.find(ctx, hookEvents, filter, key("_id", 1), nil, 0, 0)
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		if oid, ok := d["_id"].(bson.ObjectID); ok {
			ids = append(ids, oid.Hex())
		} else {
			ids = append(ids, fmt.Sprint(d["_id"]))
		}
	}
	return ids, nil
}

func (s *Store) BashEvents(ctx context.Context, q session.CommandQuery) ([]session.Doc, error) {
	command := key("$type", "string")
	if q.PatternRe != "" {
		re, err := regexFilter(q.PatternRe)
		if err != nil {
			return nil, err
		}
		command = re
	}
	filter := key("tool_name", "Bash", "tool_input.command", command, "account_id", q.AccountID)
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	projection := key("_id", 0, "session_id", 1, "tool_use_id", 1, "tool_input", 1, "created_at", 1)
	return s.find(ctx, hookEvents, filter, projection, key("created_at", -1), 0, q.Limit)
}

func (s *Store) FileEvents(ctx context.Context, q session.FileQuery) ([]session.Doc, error) {
	path := key("$type", "string")
	if q.PathRe != "" {
		re, err := regexFilter(q.PathRe)
		if err != nil {
			return nil, err
		}
		path = re
	}
	filter := key("account_id", q.AccountID, "$or", bson.A{
		key("enrichments.file-tracker.file_path", path),
		key("hook_event_name", "PostToolUse", "tool_input.file_path", path, "enrichments.edit-diff-stats", key("$exists", true)),
	})
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	projection := key("_id", 0, "session_id", 1, "hook_event_name", 1, "tool_name", 1, "tool_use_id", 1, "tool_input.file_path", 1,
		"enrichments.file-tracker", 1, "enrichments.edit-diff-stats", 1, "created_at", 1)
	return s.find(ctx, hookEvents, filter, projection, key("created_at", -1), 0, q.Limit)
}

func (s *Store) SearchTranscripts(ctx context.Context, q session.TextQuery) ([]session.Doc, error) {
	if strings.TrimSpace(q.Query) == "" {
		return []session.Doc{}, nil
	}
	filter := key("account_id", q.AccountID, "$text", key("$search", q.Query))
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	score := key("$meta", "textScore")
	projection := key("_id", 0, "session_id", 1, "seq", 1, "score", score, "enrichments.search-text", 1, "created_at", 1)
	return s.find(ctx, transcriptLines, filter, projection, key("score", score, "created_at", -1), 0, q.Limit)
}

// usageCounters are the counters of the usage enrichment a total sums.
var usageCounters = []string{"input_tokens", "cache_creation_input_tokens", "cache_creation_1h_tokens", "cache_read_input_tokens", "output_tokens"}

// UsageTotals groups the lines of each message (session and message id)
// into one, keeping the largest value of each counter, then sums the
// messages by group, model and (with LongPromptTokens) prompt length.
func (s *Store) UsageTotals(ctx context.Context, q session.UsageQuery) ([]session.UsageTotal, error) {
	match := key("account_id", q.AccountID, "enrichments.usage.message_id", key("$type", "string"))
	if len(q.SessionIDs) > 0 {
		match = append(match, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	if q.Since != "" || q.Until != "" {
		day := bson.D{}
		if q.Since != "" {
			day = append(day, bson.E{Key: "$gte", Value: q.Since})
		}
		if q.Until != "" {
			day = append(day, bson.E{Key: "$lte", Value: q.Until})
		}
		match = append(match, bson.E{Key: "enrichments.usage.day", Value: day})
	}
	perMessage := key("_id", key("s", "$session_id", "m", "$enrichments.usage.message_id"),
		"model", key("$max", "$enrichments.usage.model"),
		"day", key("$min", "$enrichments.usage.day"),
		"context_tokens", key("$max", "$enrichments.usage.context_tokens"))
	// A bare false would read as projection syntax inside the _id document.
	long := any(key("$literal", false))
	if q.LongPromptTokens > 0 {
		long = key("$gt", bson.A{key("$ifNull", bson.A{"$context_tokens", 0}), q.LongPromptTokens})
	}
	perGroup := key("_id", key("k", "$_id.s", "model", "$model", "long", long),
		"messages", key("$sum", 1),
		"peak_context_tokens", key("$max", "$context_tokens"))
	switch q.GroupBy {
	case session.UsageByModel:
		perGroup[0].Value = key("k", "$model", "model", "$model", "long", long)
	case session.UsageByDay:
		perGroup[0].Value = key("k", "$day", "model", "$model", "long", long)
	}
	out := key("_id", 0, "key", key("$ifNull", bson.A{"$_id.k", ""}), "model", key("$ifNull", bson.A{"$_id.model", ""}),
		"messages", 1, "peak_context_tokens", key("$ifNull", bson.A{"$peak_context_tokens", 0}), "long_prompt", "$_id.long")
	for _, c := range usageCounters {
		perMessage = append(perMessage, bson.E{Key: c, Value: key("$max", "$enrichments.usage."+c)})
		perGroup = append(perGroup, bson.E{Key: c, Value: key("$sum", "$"+c)})
		out = append(out, bson.E{Key: c, Value: 1})
	}
	pipeline := bson.A{
		key("$match", match),
		key("$group", perMessage),
		key("$group", perGroup),
		key("$project", out),
		key("$sort", key("key", 1, "model", 1, "long_prompt", 1)),
	}
	cur, err := s.coll(transcriptLines).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Key                      string `bson:"key"`
		Model                    string `bson:"model"`
		Messages                 int64  `bson:"messages"`
		InputTokens              int64  `bson:"input_tokens"`
		CacheCreationInputTokens int64  `bson:"cache_creation_input_tokens"`
		CacheCreation1hTokens    int64  `bson:"cache_creation_1h_tokens"`
		CacheReadInputTokens     int64  `bson:"cache_read_input_tokens"`
		OutputTokens             int64  `bson:"output_tokens"`
		PeakContextTokens        int64  `bson:"peak_context_tokens"`
		LongPrompt               bool   `bson:"long_prompt"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	totals := make([]session.UsageTotal, len(rows))
	for i, r := range rows {
		totals[i] = session.UsageTotal(r)
	}
	return totals, nil
}

func (s *Store) ErrorLines(ctx context.Context, q session.ErrorQuery) ([]session.Doc, error) {
	filter := key("account_id", q.AccountID, "enrichments.error-flag.is_error", true)
	if len(q.Types) > 0 {
		filter = append(filter, bson.E{Key: "enrichments.error-flag.error_type", Value: key("$in", q.Types)})
	}
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	projection := key("_id", 0, "session_id", 1, "seq", 1, "line", 1, "enrichments.error-flag", 1, "created_at", 1)
	return s.find(ctx, transcriptLines, filter, projection, key("created_at", -1, "seq", -1), 0, q.Limit)
}

func (s *Store) Activities(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	filter := key("account_id", q.AccountID, "enrichments.activity-kind", key("$exists", true))
	if len(q.Kinds) > 0 {
		filter = append(filter, bson.E{Key: "enrichments.activity-kind.kind", Value: key("$in", q.Kinds)})
	}
	if len(q.SessionIDs) > 0 {
		filter = append(filter, bson.E{Key: "session_id", Value: key("$in", q.SessionIDs)})
	}
	if !q.Since.IsZero() {
		filter = append(filter, bson.E{Key: "created_at", Value: key("$gte", q.Since)})
	}
	projection := key("_id", 0, "session_id", 1, "seq", 1, "line.timestamp", 1, "enrichments.activity-kind", 1, "enrichments.tool-result-detail", 1, "enrichments.system-record", 1, "created_at", 1)
	return s.find(ctx, transcriptLines, filter, projection, key("created_at", -1, "seq", -1), 0, q.Limit)
}

// --- transcript, subagent lines and blobs ---

func (s *Store) UpsertTranscriptLines(ctx context.Context, lines []session.TranscriptLine) error {
	if len(lines) == 0 {
		return nil
	}
	now := time.Now()
	models := make([]mongo.WriteModel, len(lines))
	for i, l := range lines {
		set := withRedacted(setPresent(key("session_id", l.SessionID, "seq", l.Seq, "line", l.Line), "account_id", l.AccountID), l.Redacted)
		models[i] = mongo.NewUpdateOneModel().
			SetFilter(key("session_id", l.SessionID, "seq", l.Seq)).
			SetUpdate(key("$set", withHost(set, l.Host), "$setOnInsert", key("created_at", now))).
			SetUpsert(true)
	}
	_, err := s.coll(transcriptLines).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

func (s *Store) CountTranscriptLines(ctx context.Context, accountID, sessionID string) (int, error) {
	n, err := s.coll(transcriptLines).CountDocuments(ctx, key("session_id", sessionID, "account_id", accountID))
	return int(n), err
}

func (s *Store) TranscriptLines(ctx context.Context, q session.LineQuery) ([]session.Doc, error) {
	filter := key("session_id", q.SessionID)
	if q.AccountID != "" {
		filter = append(filter, bson.E{Key: "account_id", Value: q.AccountID})
	}
	order := 1
	if q.Descending {
		order = -1
	}
	return s.find(ctx, transcriptLines, filter, key("_id", 0), key("seq", order), q.Offset, q.Limit)
}

func (s *Store) UpsertSubagentLine(ctx context.Context, l session.SubagentLine) error {
	set := withRedacted(setPresent(key("session_id", l.SessionID, "subagent_id", l.SubagentID, "seq", l.Seq, "line", l.Line), "account_id", l.AccountID), l.Redacted)
	filter := key("session_id", l.SessionID, "subagent_id", l.SubagentID, "seq", l.Seq)
	return s.upsert(ctx, subagentLines, filter, withHost(set, l.Host), key("created_at", time.Now()))
}

func (s *Store) SubagentIDs(ctx context.Context, accountID, sessionID string) ([]string, error) {
	ids := []string{}
	err := s.coll(subagentLines).Distinct(ctx, "subagent_id", key("session_id", sessionID, "account_id", accountID)).Decode(&ids)
	sort.Strings(ids)
	return ids, err
}

func (s *Store) SubagentLines(ctx context.Context, accountID, sessionID, subagentID string) ([]session.Doc, error) {
	filter := key("session_id", sessionID, "subagent_id", subagentID, "account_id", accountID)
	return s.find(ctx, subagentLines, filter, key("_id", 0), key("seq", 1), 0, 0)
}

func (s *Store) UpsertBlob(ctx context.Context, b session.Blob) error {
	set := withRedacted(setPresent(key("session_id", b.SessionID, "blob_type", b.BlobType, "name", b.Name, "content", b.Content, "encoding", b.Encoding),
		"account_id", b.AccountID), b.Redacted)
	filter := key("session_id", b.SessionID, "blob_type", b.BlobType, "name", b.Name)
	return s.upsert(ctx, blobs, filter, set, key("created_at", time.Now()))
}

func (s *Store) Blobs(ctx context.Context, accountID, sessionID string) ([]session.Doc, error) {
	return s.find(ctx, blobs, key("session_id", sessionID, "account_id", accountID), key("_id", 0), key("blob_type", 1, "name", 1), 0, 0)
}

// --- enrichment ---

func (s *Store) Unenriched(ctx context.Context, collection, enricher string, limit int) ([]session.Doc, error) {
	filter := key("enrichments."+enricher, key("$exists", false), "enrichments."+enricher+"_failed", key("$exists", false), "enrichments."+enricher+"_skipped", key("$exists", false))
	return s.find(ctx, collection, filter, nil, nil, 0, limit)
}

func (s *Store) SetEnrichment(ctx context.Context, collection string, id any, enricher string, result any) error {
	return s.update(ctx, collection, key("_id", objectID(id)), key("enrichments."+enricher, result))
}

func (s *Store) SetEnrichmentSkipped(ctx context.Context, collection string, id any, enricher string) error {
	return s.update(ctx, collection, key("_id", objectID(id)), key("enrichments."+enricher+"_skipped", true))
}

func (s *Store) RequeueSkipped(ctx context.Context, collection, accountID, enricher string) (int64, error) {
	if accountID == "" {
		return 0, session.ErrNoAccount
	}
	field := "enrichments." + enricher + "_skipped"
	res, err := s.coll(collection).UpdateMany(ctx, key("account_id", accountID, field, key("$exists", true)), key("$unset", key(field, "")))
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

func (s *Store) SetEnrichmentFailure(ctx context.Context, collection string, id any, enricher, message string, at time.Time) error {
	failure := key("message", message, "at", at)
	return s.update(ctx, collection, key("_id", objectID(id)), key("enrichments."+enricher+"_failed", failure))
}
