package sqlstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/store"
	"github.com/robbiebyrd/clued/plan/store/storetest"
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
		for _, tbl := range []string{s.plans, s.tmpls} {
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
	s, err := store.Open(config.StorageDef{Name: "db", Kind: "sqlite3"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "plans.sqlite")); err != nil {
		t.Error("default sqlite file not created in plans dir")
	}
	if _, err := store.Open(config.StorageDef{Name: "pg", Kind: "postgres"}, dir); err == nil {
		t.Error("postgres without dsn should fail")
	}
	if _, err := Open("x", "sqlite", ":memory:", "bad prefix;"); err == nil {
		t.Error("unsafe table prefix accepted")
	}
}

// Networked engines run only when a DSN is provided, e.g.
//
//	PLAN_TEST_POSTGRES_DSN=postgres://user:pass@localhost:5432/plans?sslmode=disable
//	PLAN_TEST_MYSQL_DSN=user:pass@tcp(localhost:3306)/plans
//	PLAN_TEST_CLICKHOUSE_DSN=clickhouse://localhost:9000/default
func TestPostgres(t *testing.T)   { networked(t, "postgres", "PLAN_TEST_POSTGRES_DSN") }
func TestMySQL(t *testing.T)      { networked(t, "mysql", "PLAN_TEST_MYSQL_DSN") }
func TestClickHouse(t *testing.T) { networked(t, "clickhouse", "PLAN_TEST_CLICKHOUSE_DSN") }

func networked(t *testing.T, kind, env string) {
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run the %s conformance suite", env, kind)
	}
	storetest.Run(t, func(t *testing.T) store.Store { return open(t, kind, dsn) })
}
