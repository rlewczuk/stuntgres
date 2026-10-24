package reference_test

import (
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameControlsOwner(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rename_control_owner"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s")
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE USER rename_control_owner", 0))
	if p.Postgres != nil {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("CREATE USER rename_control_owner", 0))
	}
	c, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer c.Close()
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"CREATE SCHEMA s AUTHORIZATION rename_control_owner", "SET SESSION AUTHORIZATION rename_control_owner", "CREATE TABLE s.t(a INT CONSTRAINT nn NOT NULL,b TEXT)", "INSERT INTO s.t VALUES(7,'O''Brien'),(8,NULL)", "COMMENT ON TABLE s.t IS 'table comment'", "COMMENT ON COLUMN s.t.a IS 'column comment'", "ALTER TABLE s.t RENAME a TO renamed", "ALTER TABLE s.t RENAME TO newtable", "RESET SESSION AUTHORIZATION"} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 2
			}
			fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec(q, count))
		}
	}()
	canonical := referenceDDLNativeDump(t, p)
	if !strings.Contains(canonical, `CREATE SCHEMA "s" AUTHORIZATION "rename_control_owner";`) || !strings.Contains(canonical, `SET SESSION AUTHORIZATION 'rename_control_owner';`) {
		t.Fatal(canonical)
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(canonical)); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		oracle, e := p.Postgres.Conn(p.Ctx)
		if e != nil {
			t.Fatal(e)
		}
		func() {
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, e = oracle.ExecContext(p.Ctx, canonical); e != nil {
				t.Fatal("owner-bearing PostgreSQL replay", e)
			}
		}()
		var owner string
		if e = p.Postgres.QueryRowContext(p.Ctx, `SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='s.newtable'::regclass`).Scan(&owner); e != nil || owner != "rename_control_owner" {
			t.Fatal(owner, e)
		}
	}
	for _, backend := range p.Backends() {
		func() {
			conn, e := backend.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION rename_control_owner", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT * FROM s.newtable ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "O'Brien"}, {int64(8), nil}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("COMMENT ON COLUMN s.newtable.renamed IS 'column comment'", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}()
	}
	if referenceDDLNativeDump(t, p) != canonical {
		t.Fatal("owner roundtrip changed canonical bytes")
	}
}

func TestReferenceAlterRenameTransactionBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("ALTER TABLE t RENAME TO blocked", 0))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Select("SELECT * FROM blocked", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
