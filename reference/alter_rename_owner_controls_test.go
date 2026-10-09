package reference_test

import (
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterRenameControlsOwner(t *testing.T) {
	roleReferenceCleanup(t, []string{"rename_control_owner"})
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s")
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE USER rename_control_owner", 0))
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("CREATE USER rename_control_owner", 0))
	}
	c, err := p.memory.Conn(p.ctx)
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
			referenceRunStep(t, p.ctx, c, p.memoryRecord, referenceExec(q, count))
		}
	}()
	canonical := referenceDDLNativeDump(t, p)
	if !strings.Contains(canonical, `CREATE SCHEMA "s" AUTHORIZATION "rename_control_owner";`) || !strings.Contains(canonical, `SET SESSION AUTHORIZATION 'rename_control_owner';`) {
		t.Fatal(canonical)
	}
	if err = stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(canonical)); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		oracle, e := p.postgres.Conn(p.ctx)
		if e != nil {
			t.Fatal(e)
		}
		func() {
			defer oracle.Close()
			defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, e = oracle.ExecContext(p.ctx, canonical); e != nil {
				t.Fatal("owner-bearing PostgreSQL replay", e)
			}
		}()
		var owner string
		if e = p.postgres.QueryRowContext(p.ctx, `SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='s.newtable'::regclass`).Scan(&owner); e != nil || owner != "rename_control_owner" {
			t.Fatal(owner, e)
		}
	}
	for _, backend := range p.backends() {
		func() {
			conn, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET SESSION AUTHORIZATION rename_control_owner", 0))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT * FROM s.newtable ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "O'Brien"}, {int64(8), nil}}))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("COMMENT ON COLUMN s.newtable.renamed IS 'column comment'", 0))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("RESET SESSION AUTHORIZATION", 0))
		}()
	}
	if referenceDDLNativeDump(t, p) != canonical {
		t.Fatal("owner roundtrip changed canonical bytes")
	}
}

func TestReferenceAlterRenameTransactionBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	for _, backend := range p.backends() {
		func() {
			tx, err := backend.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if backend.pg {
				referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("ALTER TABLE t RENAME TO blocked", 0))
				referenceRunStep(t, p.ctx, tx, backend.recorder, referenceSelect("SELECT * FROM blocked", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			} else {
				referenceRunStep(t, p.ctx, tx, backend.recorder, roleReferenceError("ALTER TABLE t RENAME TO blocked", "0A000", "statement is unsupported in an explicit transaction", "", false, "execute"))
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}()
	}
}
