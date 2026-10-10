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
	sessionFull     = "session_full"

	maxSessions = 500

	// namespaceExists is the server error code for creating a collection that exists.
	namespaceExists = 48
)

// Store is a MongoDB-backed session.Store. Connect must be called before use.
type Store struct {
	// Logger receives index warnings; nil means slog.Default().
	Logger *slog.Logger

	url    string
	dbName string
	client *mongo.Client
	db     *mongo.Database
}

var _ session.Store = (*Store)(nil)

// New prepares a store for cfg.MongoURL and cfg.DBName; the connection is made by Connect.
func New(cfg session.Config) *Store {
	return &Store{url: cfg.MongoURL, dbName: cfg.DBName}
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
	return s.client.Disconnect(ctx)
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
		return x.Time()
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

func (s *Store) GetSession(ctx context.Context, accountID, sessionID string) (session.Doc, error) {
	return s.findOne(ctx, sessions, key("session_id", sessionID, "account_id", accountID))
}

func (s *Store) FullSession(ctx context.Context, accountID, sessionID string) (session.Doc, error) {
	return s.findOne(ctx, sessionFull, key("session_id", sessionID, "account_id", accountID))
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
	for _, f := range []struct{ field, pattern string }{{"project_path", q.ProjectPathRe}, {"git_origin", q.GitOriginRe}} {
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
	limit := q.Limit
	if limit <= 0 || limit > maxSessions {
		limit = maxSessions
	}
	projection := key("_id", 0, "session_id", 1, "project_path", 1, "git_origin", 1, "cwd", 1, "started_at", 1, "last_seen", 1)
	return s.find(ctx, sessions, filter, projection, key("last_seen", -1), 0, limit)
}

// --- hook events ---

func (s *Store) InsertHookEvent(ctx context.Context, ev session.Doc) error {
	_, err := s.coll(hookEvents).InsertOne(ctx, ev)
	return err
}

func (s *Store) HookEventIDsByToolUse(ctx context.Context, sessionID string, toolUseIDs []string) ([]string, error) {
	ids := []string{}
	if len(toolUseIDs) == 0 {
		return ids, nil
	}
	filter := key("tool_use_id", key("$in", toolUseIDs), "session_id", sessionID)
	docs, err := s.find(ctx, hookEvents, filter, key("_id", 1), nil, 0, 0)
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		ids = append(ids, d["_id"].(bson.ObjectID).Hex())
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
	projection := key("_id", 0, "session_id", 1, "tool_input", 1, "created_at", 1)
	return s.find(ctx, hookEvents, filter, projection, key("created_at", -1), 0, q.Limit)
}

// --- transcript, subagent lines and blobs ---

func (s *Store) UpsertTranscriptLines(ctx context.Context, lines []session.TranscriptLine) error {
	if len(lines) == 0 {
		return nil
	}
	now := time.Now()
	models := make([]mongo.WriteModel, len(lines))
	for i, l := range lines {
		set := setPresent(key("session_id", l.SessionID, "seq", l.Seq, "line", l.Line), "account_id", l.AccountID)
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
	set := setPresent(key("session_id", l.SessionID, "subagent_id", l.SubagentID, "seq", l.Seq, "line", l.Line), "account_id", l.AccountID)
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
	set := setPresent(key("session_id", b.SessionID, "blob_type", b.BlobType, "name", b.Name, "content", b.Content, "encoding", b.Encoding),
		"account_id", b.AccountID)
	filter := key("session_id", b.SessionID, "blob_type", b.BlobType, "name", b.Name)
	return s.upsert(ctx, blobs, filter, set, key("created_at", time.Now()))
}

func (s *Store) Blobs(ctx context.Context, accountID, sessionID string) ([]session.Doc, error) {
	return s.find(ctx, blobs, key("session_id", sessionID, "account_id", accountID), key("_id", 0), key("blob_type", 1, "name", 1), 0, 0)
}

// --- enrichment ---

func (s *Store) Unenriched(ctx context.Context, collection, enricher string, limit int) ([]session.Doc, error) {
	filter := key("enrichments."+enricher, key("$exists", false), "enrichments."+enricher+"_failed", key("$exists", false))
	return s.find(ctx, collection, filter, nil, nil, 0, limit)
}

func (s *Store) SetEnrichment(ctx context.Context, collection string, id any, enricher string, result any) error {
	return s.update(ctx, collection, key("_id", objectID(id)), key("enrichments."+enricher, result))
}

func (s *Store) SetEnrichmentFailure(ctx context.Context, collection string, id any, enricher, message string, at time.Time) error {
	failure := key("message", message, "at", at)
	return s.update(ctx, collection, key("_id", objectID(id)), key("enrichments."+enricher+"_failed", failure))
}
