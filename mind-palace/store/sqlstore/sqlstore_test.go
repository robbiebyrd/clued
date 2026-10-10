package sqlstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/storetest"
)

func open(t *testing.T, kind, dsn string) store.Store {
	t.Helper()
	s, err := Open("test", kind, dsn, "t"+time.Now().Format("150405")+"_")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("init %s: %v", kind, err)
	}
	t.Cleanup(func() {
		for _, tbl := range []string{s.prefix + "plans", s.prefix + "stories", s.tmpls} {
			s.db.Exec("DROP TABLE IF EXISTS " + tbl)
		}
		s.Close()
	})
	return s
}

func TestSQLite(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		return open(t, "sqlite", filepath.Join(t.TempDir(), "plans.sqlite"))
	})
}

func TestSQLiteFactoryDefaultsDSN(t *testing.T) {
	dir := t.TempDir()
	dirs := map[string]string{"plan": filepath.Join(dir, "docs", "plans"), "story": filepath.Join(dir, "docs", "stories")}
	s, err := store.Open(config.StorageDef{Name: "db", Kind: "sqlite3"}, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "docs", DefaultSQLiteFile)); err != nil {
		t.Error("default sqlite file not created next to the plans dir")
	}
	if _, err := store.Open(config.StorageDef{Name: "pg", Kind: "postgres"}, dirs); err == nil {
		t.Error("postgres without dsn should fail")
	}
	if _, err := Open("x", "sqlite", ":memory:", "bad prefix;"); err == nil {
		t.Error("unsafe table prefix accepted")
	}
}

// Networked engines run only when a DSN is provided, e.g.
//
//	MIND_PALACE_TEST_POSTGRES_DSN=postgres://user:pass@localhost:5432/plans?sslmode=disable
//	MIND_PALACE_TEST_MYSQL_DSN=user:pass@tcp(localhost:3306)/plans
//	MIND_PALACE_TEST_CLICKHOUSE_DSN=clickhouse://localhost:9000/default
func TestPostgres(t *testing.T)   { networked(t, "postgres", "MIND_PALACE_TEST_POSTGRES_DSN") }
func TestMySQL(t *testing.T)      { networked(t, "mysql", "MIND_PALACE_TEST_MYSQL_DSN") }
func TestClickHouse(t *testing.T) { networked(t, "clickhouse", "MIND_PALACE_TEST_CLICKHOUSE_DSN") }

func networked(t *testing.T, kind, env string) {
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run the %s conformance suite", env, kind)
	}
	storetest.Run(t, func(t *testing.T) store.Store { return open(t, kind, dsn) })
}
