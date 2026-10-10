// Package sqlstore stores documents in a relational database through
// database/sql. One implementation serves SQLite, PostgreSQL, MySQL/MariaDB
// and ClickHouse via small dialect differences (placeholders, upsert syntax,
// DDL).
//
// Each kind gets its own table (<prefix>plans, <prefix>stories); templates
// of every kind share <prefix>templates keyed by (kind, id). A document row
// carries the indexed scalars (path, type, status, priority, title, updated)
// and the canonical Markdown document, which is parsed on read so every
// store yields the same document.
//
// Options:
//
//	dsn          connection string (required; SQLite defaults to <docs>/mind-palace.sqlite
//	             next to the configured plans directory)
//	tablePrefix  table name prefix (default "mp_")
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	// Drivers register themselves on import.
	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Dialect captures what differs between engines.
type Dialect struct {
	Kind   string
	Driver string
	// Placeholder returns the parameter marker for position n (1-based).
	Placeholder func(n int) string
	// Upsert reports whether INSERT … ON CONFLICT/ON DUPLICATE is available
	// (ClickHouse uses ReplacingMergeTree versions instead).
	Upsert       string // "conflict", "duplicate" or "replacing"
	DocumentsDDL string
	TemplatesDDL string
	// Final is appended to SELECTs (ClickHouse FINAL).
	Final string
}

var dialects = map[string]*Dialect{
	"sqlite": {
		Kind: "sqlite", Driver: "sqlite", Placeholder: qmark, Upsert: "conflict",
		DocumentsDDL: `CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, path TEXT NOT NULL, type TEXT NOT NULL, status TEXT NOT NULL, priority TEXT NOT NULL, title TEXT NOT NULL, updated TEXT NOT NULL, document TEXT NOT NULL)`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (kind TEXT NOT NULL, id TEXT NOT NULL, content TEXT NOT NULL, updated TEXT NOT NULL, PRIMARY KEY (kind, id))`,
	},
	"postgres": {
		Kind: "postgres", Driver: "pgx", Placeholder: dollar, Upsert: "conflict",
		DocumentsDDL: `CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, path TEXT NOT NULL, type TEXT NOT NULL, status TEXT NOT NULL, priority TEXT NOT NULL, title TEXT NOT NULL, updated TEXT NOT NULL, document TEXT NOT NULL)`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (kind TEXT NOT NULL, id TEXT NOT NULL, content TEXT NOT NULL, updated TEXT NOT NULL, PRIMARY KEY (kind, id))`,
	},
	"mysql": {
		Kind: "mysql", Driver: "mysql", Placeholder: qmark, Upsert: "duplicate",
		DocumentsDDL: `CREATE TABLE IF NOT EXISTS %s (id VARCHAR(16) PRIMARY KEY, path VARCHAR(768) NOT NULL, type VARCHAR(16) NOT NULL, status VARCHAR(64) NOT NULL, priority VARCHAR(16) NOT NULL, title TEXT NOT NULL, updated VARCHAR(32) NOT NULL, document LONGTEXT NOT NULL) CHARACTER SET utf8mb4`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (kind VARCHAR(32) NOT NULL, id VARCHAR(128) NOT NULL, content LONGTEXT NOT NULL, updated VARCHAR(32) NOT NULL, PRIMARY KEY (kind, id)) CHARACTER SET utf8mb4`,
	},
	"clickhouse": {
		Kind: "clickhouse", Driver: "clickhouse", Placeholder: qmark, Upsert: "replacing", Final: " FINAL",
		DocumentsDDL: `CREATE TABLE IF NOT EXISTS %s (id String, path String, type String, status String, priority String, title String, updated String, document String, version UInt64) ENGINE = ReplacingMergeTree(version) ORDER BY id`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (kind String, id String, content String, updated String, version UInt64) ENGINE = ReplacingMergeTree(version) ORDER BY (kind, id)`,
	},
}

// Aliases accepted as storage kinds.
var aliases = map[string]string{"sqlite3": "sqlite", "postgresql": "postgres", "pgx": "postgres", "mariadb": "mysql"}

// identRe limits table prefixes to plain identifiers (they are interpolated into SQL).
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DefaultSQLiteFile is the SQLite file name used when no dsn is configured.
const DefaultSQLiteFile = "mind-palace.sqlite"

func qmark(int) string    { return "?" }
func dollar(n int) string { return fmt.Sprintf("$%d", n) }

func init() {
	for k := range dialects {
		store.Register(k, factory(k))
	}
	for alias, k := range aliases {
		store.Register(alias, factory(k))
	}
}

func factory(driver string) store.Factory {
	return func(def config.StorageDef, dirs map[string]string) (store.Store, error) {
		dsn := def.Options["dsn"]
		if dsn == "" {
			if driver == "sqlite" {
				dsn = DefaultSQLitePath(dirs)
			} else {
				return nil, fmt.Errorf("storage %q: option dsn is required for %s", def.Name, driver)
			}
		}
		return Open(def.Name, driver, dsn, def.Options["tablePrefix"])
	}
}

// DefaultSQLitePath places the database next to the plans directory
// (docs/mind-palace.sqlite with the default layout).
func DefaultSQLitePath(dirs map[string]string) string {
	base := dirs[kind.Plan]
	if base == "" {
		base = kind.Must(kind.Plan).DefaultDir
	}
	return filepath.Join(filepath.Dir(filepath.Clean(base)), DefaultSQLiteFile)
}

// Store is a SQL-backed document store.
type Store struct {
	name    string
	dialect *Dialect
	dsn     string
	db      *sql.DB
	prefix  string
	tmpls   string
}

// Open prepares a store (the connection is made in Init).
func Open(name, driver, dsn, tablePrefix string) (*Store, error) {
	if k, ok := aliases[driver]; ok {
		driver = k
	}
	d, ok := dialects[driver]
	if !ok {
		return nil, fmt.Errorf("unknown sql dialect %q", driver)
	}
	if tablePrefix == "" {
		tablePrefix = "mp_"
	}
	if !identRe.MatchString(tablePrefix) {
		return nil, fmt.Errorf("tablePrefix %q must be letters, digits and underscores", tablePrefix)
	}
	if name == "" {
		name = driver
	}
	return &Store{name: name, dialect: d, dsn: dsn, prefix: tablePrefix, tmpls: tablePrefix + "templates"}, nil
}

// DB exposes the connection (after Init).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Name() string   { return s.name }
func (s *Store) Driver() string { return s.dialect.Kind }

// table returns the document table of a kind.
func (s *Store) table(kindName string) (string, error) {
	k, ok := kind.Get(kindName)
	if !ok {
		return "", fmt.Errorf("unknown document kind %q", kindName)
	}
	return s.prefix + k.Plural, nil
}

func (s *Store) Init(ctx context.Context) error {
	if s.db == nil {
		db, err := sql.Open(s.dialect.Driver, s.dsn)
		if err != nil {
			return err
		}
		if s.dialect.Kind == "sqlite" {
			db.SetMaxOpenConns(1)
			if dir := filepath.Dir(s.dsn); s.dsn != ":memory:" && !strings.HasPrefix(s.dsn, "file:") {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
		}
		s.db = db
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect %s: %w", s.dialect.Kind, err)
	}
	for _, k := range kind.All() {
		table := s.prefix + k.Plural
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(s.dialect.DocumentsDDL, table)); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(s.dialect.TemplatesDDL, s.tmpls)); err != nil {
		return fmt.Errorf("create %s: %w", s.tmpls, err)
	}
	return nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) ph(n int) string { return s.dialect.Placeholder(n) }

func (s *Store) List(ctx context.Context, kindName string) ([]*model.Document, error) {
	table, err := s.table(kindName)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT path, document FROM %s%s ORDER BY id", table, s.dialect.Final))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Document
	for rows.Next() {
		var path, doc string
		if err := rows.Scan(&path, &doc); err != nil {
			return nil, err
		}
		d, err := decode(kindName, path, doc)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, kindName, id string) (*model.Document, error) {
	table, err := s.table(kindName)
	if err != nil {
		return nil, err
	}
	var path, doc string
	q := fmt.Sprintf("SELECT path, document FROM %s%s WHERE id = %s", table, s.dialect.Final, s.ph(1))
	err = s.db.QueryRowContext(ctx, q, id).Scan(&path, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(kindName, path, doc)
}

func decode(kindName, path, doc string) (*model.Document, error) {
	fm, content, err := markdown.Parse(doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &model.Document{Kind: kindName, Path: path, FrontMatter: fm, Content: content}, nil
}

func (s *Store) Put(ctx context.Context, d *model.Document) error {
	table, err := s.table(d.Kind)
	if err != nil {
		return err
	}
	doc, err := markdown.RenderDocument(d)
	if err != nil {
		return err
	}
	fm := d.FrontMatter
	cols := []string{"id", "path", "type", "status", "priority", "title", "updated", "document"}
	args := []any{fm.ID, d.Path, fm.Type, fm.Status, fm.Priority, fm.Title, fm.Updated, doc}
	return s.upsert(ctx, table, 1, cols, args)
}

// upsert writes a row; the first keyCols columns form the key.
func (s *Store) upsert(ctx context.Context, table string, keyCols int, cols []string, args []any) error {
	if s.dialect.Upsert == "replacing" {
		cols = append(cols, "version")
		args = append(args, uint64(time.Now().UnixNano()))
	}
	marks := make([]string, len(cols))
	for i := range cols {
		marks[i] = s.ph(i + 1)
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(marks, ", "))
	switch s.dialect.Upsert {
	case "conflict":
		var sets []string
		for _, c := range cols[keyCols:] {
			sets = append(sets, fmt.Sprintf("%s = excluded.%s", c, c))
		}
		q += fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s", strings.Join(cols[:keyCols], ", "), strings.Join(sets, ", "))
	case "duplicate":
		var sets []string
		for _, c := range cols[keyCols:] {
			sets = append(sets, fmt.Sprintf("%s = VALUES(%s)", c, c))
		}
		q += " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

func (s *Store) Delete(ctx context.Context, kindName, id string) error {
	table, err := s.table(kindName)
	if err != nil {
		return err
	}
	return s.deleteRows(ctx, table, "id = "+s.ph(1), id)
}

// deleteRows removes the rows matching where; ErrNotFound when none matched.
func (s *Store) deleteRows(ctx context.Context, table, where string, args ...any) error {
	if s.dialect.Upsert == "replacing" {
		// ClickHouse: confirm existence first; DELETE reports no affected rows.
		var n int
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT count() FROM %s FINAL WHERE %s", table, where), args...).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return store.ErrNotFound
		}
		_, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", table, where), args...)
		return err
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", table, where), args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListTemplates(ctx context.Context, kindName string) ([]*model.Template, error) {
	q := fmt.Sprintf("SELECT kind, id, content, updated FROM %s%s WHERE kind = %s ORDER BY id", s.tmpls, s.dialect.Final, s.ph(1))
	rows, err := s.db.QueryContext(ctx, q, kindName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Template
	for rows.Next() {
		var t model.Template
		if err := rows.Scan(&t.Kind, &t.ID, &t.Content, &t.Updated); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *Store) GetTemplate(ctx context.Context, kindName, id string) (*model.Template, error) {
	var t model.Template
	q := fmt.Sprintf("SELECT kind, id, content, updated FROM %s%s WHERE kind = %s AND id = %s", s.tmpls, s.dialect.Final, s.ph(1), s.ph(2))
	err := s.db.QueryRowContext(ctx, q, kindName, id).Scan(&t.Kind, &t.ID, &t.Content, &t.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) PutTemplate(ctx context.Context, t *model.Template) error {
	updated := t.Updated
	if updated == "" {
		updated = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return s.upsert(ctx, s.tmpls, 2, []string{"kind", "id", "content", "updated"}, []any{t.Kind, t.ID, t.Content, updated})
}

func (s *Store) DeleteTemplate(ctx context.Context, kindName, id string) error {
	return s.deleteRows(ctx, s.tmpls, fmt.Sprintf("kind = %s AND id = %s", s.ph(1), s.ph(2)), kindName, id)
}

var _ store.Store = (*Store)(nil)
