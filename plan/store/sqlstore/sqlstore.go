// Package sqlstore stores plans in a relational database through database/sql.
// One implementation serves SQLite, PostgreSQL, MySQL/MariaDB and ClickHouse
// via small dialect differences (placeholders, upsert syntax, DDL).
//
// Each plan row carries the indexed scalars (path, type, status, priority,
// title, updated) and the canonical Markdown document, which is parsed on
// read so every store yields the same plan.
//
// Options:
//
//	dsn          connection string (required; SQLite defaults to <plansDir>/plans.sqlite)
//	tablePrefix  table name prefix (default "plan_")
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	// Drivers register themselves on import.
	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/markdown"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
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
	PlansDDL     string
	TemplatesDDL string
	// Final is appended to SELECTs (ClickHouse FINAL).
	Final string
}

var dialects = map[string]*Dialect{
	"sqlite": {
		Kind: "sqlite", Driver: "sqlite", Placeholder: qmark, Upsert: "conflict",
		PlansDDL:     `CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, path TEXT NOT NULL, type TEXT NOT NULL, status TEXT NOT NULL, priority TEXT NOT NULL, title TEXT NOT NULL, updated TEXT NOT NULL, document TEXT NOT NULL)`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, content TEXT NOT NULL, updated TEXT NOT NULL)`,
	},
	"postgres": {
		Kind: "postgres", Driver: "pgx", Placeholder: dollar, Upsert: "conflict",
		PlansDDL:     `CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, path TEXT NOT NULL, type TEXT NOT NULL, status TEXT NOT NULL, priority TEXT NOT NULL, title TEXT NOT NULL, updated TEXT NOT NULL, document TEXT NOT NULL)`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, content TEXT NOT NULL, updated TEXT NOT NULL)`,
	},
	"mysql": {
		Kind: "mysql", Driver: "mysql", Placeholder: qmark, Upsert: "duplicate",
		PlansDDL:     `CREATE TABLE IF NOT EXISTS %s (id VARCHAR(16) PRIMARY KEY, path VARCHAR(768) NOT NULL, type VARCHAR(16) NOT NULL, status VARCHAR(64) NOT NULL, priority VARCHAR(16) NOT NULL, title TEXT NOT NULL, updated VARCHAR(32) NOT NULL, document LONGTEXT NOT NULL) CHARACTER SET utf8mb4`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (id VARCHAR(128) PRIMARY KEY, content LONGTEXT NOT NULL, updated VARCHAR(32) NOT NULL) CHARACTER SET utf8mb4`,
	},
	"clickhouse": {
		Kind: "clickhouse", Driver: "clickhouse", Placeholder: qmark, Upsert: "replacing", Final: " FINAL",
		PlansDDL:     `CREATE TABLE IF NOT EXISTS %s (id String, path String, type String, status String, priority String, title String, updated String, document String, version UInt64) ENGINE = ReplacingMergeTree(version) ORDER BY id`,
		TemplatesDDL: `CREATE TABLE IF NOT EXISTS %s (id String, content String, updated String, version UInt64) ENGINE = ReplacingMergeTree(version) ORDER BY id`,
	},
}

// Aliases accepted as storage kinds.
var aliases = map[string]string{"sqlite3": "sqlite", "postgresql": "postgres", "pgx": "postgres", "mariadb": "mysql"}

// identRe limits table prefixes to plain identifiers (they are interpolated into SQL).
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func qmark(int) string    { return "?" }
func dollar(n int) string { return fmt.Sprintf("$%d", n) }

func init() {
	for kind := range dialects {
		store.Register(kind, factory(kind))
	}
	for alias, kind := range aliases {
		store.Register(alias, factory(kind))
	}
}

func factory(kind string) store.Factory {
	return func(def config.StorageDef, plansDir string) (store.Store, error) {
		dsn := def.Options["dsn"]
		if dsn == "" {
			if kind == "sqlite" {
				dsn = filepath.Join(plansDir, "plans.sqlite")
			} else {
				return nil, fmt.Errorf("storage %q: option dsn is required for %s", def.Name, kind)
			}
		}
		return Open(def.Name, kind, dsn, def.Options["tablePrefix"])
	}
}

// Store is a SQL-backed plan store.
type Store struct {
	name    string
	dialect *Dialect
	dsn     string
	db      *sql.DB
	plans   string
	tmpls   string
}

// Open prepares a store (the connection is made in Init).
func Open(name, kind, dsn, tablePrefix string) (*Store, error) {
	if k, ok := aliases[kind]; ok {
		kind = k
	}
	d, ok := dialects[kind]
	if !ok {
		return nil, fmt.Errorf("unknown sql dialect %q", kind)
	}
	if tablePrefix == "" {
		tablePrefix = "plan_"
	}
	if !identRe.MatchString(tablePrefix) {
		return nil, fmt.Errorf("tablePrefix %q must be letters, digits and underscores", tablePrefix)
	}
	if name == "" {
		name = kind
	}
	return &Store{name: name, dialect: d, dsn: dsn, plans: tablePrefix + "plans", tmpls: tablePrefix + "templates"}, nil
}

// DB exposes the connection (after Init).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Name() string { return s.name }
func (s *Store) Kind() string { return s.dialect.Kind }

func (s *Store) Init(ctx context.Context) error {
	if s.db == nil {
		db, err := sql.Open(s.dialect.Driver, s.dsn)
		if err != nil {
			return err
		}
		if s.dialect.Kind == "sqlite" {
			db.SetMaxOpenConns(1)
		}
		s.db = db
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect %s: %w", s.dialect.Kind, err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(s.dialect.PlansDDL, s.plans)); err != nil {
		return fmt.Errorf("create %s: %w", s.plans, err)
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

func (s *Store) ListPlans(ctx context.Context) ([]*model.Plan, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT path, document FROM %s%s ORDER BY id", s.plans, s.dialect.Final))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Plan
	for rows.Next() {
		var path, doc string
		if err := rows.Scan(&path, &doc); err != nil {
			return nil, err
		}
		p, err := decode(path, doc)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPlan(ctx context.Context, id string) (*model.Plan, error) {
	var path, doc string
	q := fmt.Sprintf("SELECT path, document FROM %s%s WHERE id = %s", s.plans, s.dialect.Final, s.ph(1))
	err := s.db.QueryRowContext(ctx, q, id).Scan(&path, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(path, doc)
}

func decode(path, doc string) (*model.Plan, error) {
	fm, content, err := markdown.Parse(doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &model.Plan{Path: path, FrontMatter: fm, Content: content}, nil
}

func (s *Store) PutPlan(ctx context.Context, p *model.Plan) error {
	doc, err := markdown.RenderPlan(p)
	if err != nil {
		return err
	}
	fm := p.FrontMatter
	cols := []string{"id", "path", "type", "status", "priority", "title", "updated", "document"}
	args := []any{fm.ID, p.Path, fm.Type, fm.Status, fm.Priority, fm.Title, fm.Updated, doc}
	return s.upsert(ctx, s.plans, cols, args)
}

// upsert writes a row keyed by the first column.
func (s *Store) upsert(ctx context.Context, table string, cols []string, args []any) error {
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
		for _, c := range cols[1:] {
			sets = append(sets, fmt.Sprintf("%s = excluded.%s", c, c))
		}
		q += fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s", cols[0], strings.Join(sets, ", "))
	case "duplicate":
		var sets []string
		for _, c := range cols[1:] {
			sets = append(sets, fmt.Sprintf("%s = VALUES(%s)", c, c))
		}
		q += " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

func (s *Store) DeletePlan(ctx context.Context, id string) error {
	return s.deleteRow(ctx, s.plans, id)
}

func (s *Store) deleteRow(ctx context.Context, table, id string) error {
	if s.dialect.Upsert == "replacing" {
		// ClickHouse: confirm existence first; DELETE reports no affected rows.
		var n int
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT count() FROM %s FINAL WHERE id = %s", table, s.ph(1)), id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return store.ErrNotFound
		}
		_, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = %s", table, s.ph(1)), id)
		return err
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = %s", table, s.ph(1)), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListTemplates(ctx context.Context) ([]*model.Template, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT id, content, updated FROM %s%s ORDER BY id", s.tmpls, s.dialect.Final))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Template
	for rows.Next() {
		var t model.Template
		if err := rows.Scan(&t.ID, &t.Content, &t.Updated); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *Store) GetTemplate(ctx context.Context, id string) (*model.Template, error) {
	var t model.Template
	q := fmt.Sprintf("SELECT id, content, updated FROM %s%s WHERE id = %s", s.tmpls, s.dialect.Final, s.ph(1))
	err := s.db.QueryRowContext(ctx, q, id).Scan(&t.ID, &t.Content, &t.Updated)
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
	return s.upsert(ctx, s.tmpls, []string{"id", "content", "updated"}, []any{t.ID, t.Content, updated})
}

func (s *Store) DeleteTemplate(ctx context.Context, id string) error {
	return s.deleteRow(ctx, s.tmpls, id)
}

var _ store.Store = (*Store)(nil)
