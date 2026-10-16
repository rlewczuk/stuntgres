package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTablespaceControls(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		atsControlWitness(t, p, b, c)
	})
}

func atsControlWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var original bytes.Buffer
	var held *sql.Rows
	var err error
	if !b.PG {
		held, err = p.Native.QueryContext(p.Ctx, "SELECT id,note FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
	}
	if !b.PG {
		if err := stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
			t.Fatal(err)
		}
	}
	atsExec(t, p, b, c, "ALTER TABLE t SET TABLESPACE pg_default")
	if !b.PG {
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || unchanged.String() != original.String() {
			t.Fatal("no-op changed canonical dump", err, unchanged.String(), original.String())
		}
	}
	atsError(t, p, b, c, "ALTER TABLE t ADD COLUMN lost INT DEFAULT 8, SET TABLESPACE ats_missing", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`))
	if !b.PG {
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || unchanged.String() != original.String() {
			t.Fatal("failure changed dump", err)
		}
	}
	atsExec(t, p, b, c, "ALTER TABLE t ADD COLUMN extra INT DEFAULT 9, SET TABLESPACE pg_default")
	atsRun(t, p, b, c, fixture.Select("SELECT id,note,extra FROM t", []string{"id", "note", "extra"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "keep", int64(9)}}))
	if b.PG {
		return
	}
	atsExec(t, p, b, c, "CREATE TEMP TABLE ats_dump_temp(a INT)")
	var dump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"placement witness", "fallback", "ats_nn", "extra"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal("missing persistent definition", fragment, dump.String())
		}
	}
	if strings.Contains(dump.String(), "TABLESPACE") || strings.Contains(dump.String(), "ats_dump_temp") {
		t.Fatal("dump contains physical history or TEMP", dump.String())
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err = stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var afterCancel bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &afterCancel); err != nil || afterCancel.String() != dump.String() {
		t.Fatal("canceled restore changed target", err)
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nALTER TABLE t SET TABLESPACE ats_missing;")); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	var afterFailure bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &afterFailure); err != nil || afterFailure.String() != dump.String() {
		t.Fatal("failed restore changed target", err)
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("canonical restore", err, redump.String(), dump.String())
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var id int64
	var note string
	if err = held.Scan(&id, &note); err != nil || id != 7 || note != "keep" {
		t.Fatal(id, note, err)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal(held.Err())
	}
	held.Close()
	atsRun(t, p, b, b.DB, fixture.Select("SELECT id,note,extra FROM t", []string{"id", "note", "extra"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "keep", int64(9)}}))
	atsRun(t, p, b, b.DB, fixture.Exec("INSERT INTO t(id) VALUES(8)", 1))
	atsRun(t, p, b, b.DB, fixture.Select("SELECT note,extra FROM t WHERE id=8", []string{"note", "extra"}, []string{"TEXT", "INT4"}, [][]any{{"fallback", int64(9)}}))
}

func TestReferenceAlterTablespaceFeatureSmoke(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		atsExec(t, p, b, c, "CREATE ROLE ats_reader")
		atsExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO ats_reader")
		q := "SELECT id,note FROM t ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("ALTER TABLE t SET TABLESPACE pg_default", 0)
			s.Prepared = prepared
			atsRun(t, p, b, c, s)
			atsRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, atsRows())
		}
		atsPlacement(t, p, b, c, "t", 0)
		atsRun(t, p, b, c, fixture.Select("SELECT tablespace FROM pg_tables WHERE schemaname='public' AND tablename='t'", []string{"tablespace"}, []string{"NAME"}, [][]any{{nil}}))
		atsError(t, p, b, c, "ALTER TABLE t SET TABLESPACE ats_missing", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`))
		atsError(t, p, b, c, "ALTER TABLE t SET TABLESPACE pg_global", atsDiagnostic("22023", "only shared relations can be placed in pg_global tablespace"))
		atsError(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE pg_global", atsDiagnostic("22023", "cannot move relations in to or out of pg_global tablespace"))
		atsError(t, p, b, c, "ALTER TABLE t SET TABLESPACE pg_default, SET TABLESPACE pg_default", atsDiagnostic("42601", "cannot have multiple SET TABLESPACE subcommands"))
		atsExec(t, p, b, c, "SET ROLE ats_reader")
		atsError(t, p, b, c, "ALTER TABLE t SET TABLESPACE pg_default", atsDiagnostic("42501", "must be owner of table t"))
		atsExec(t, p, b, c, "RESET ROLE")
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		atsExec(t, p, b, tx, "LOCK TABLE t IN ACCESS SHARE MODE")
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec("ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT", 0))
		if len(notices) != 0 {
			t.Fatal(notices)
		}
		atsExec(t, p, b, c, "SET lock_timeout='100ms'")
		atsError(t, p, b, c, "ALTER TABLE t SET TABLESPACE pg_default", atsDiagnostic("55P03", "canceling statement due to lock timeout"))
		atsExec(t, p, b, c, "RESET lock_timeout")
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		// Remove the reader's actual schema ACL before the dumpable control phase.
		atsExec(t, p, b, c, "REVOKE USAGE ON SCHEMA public FROM ats_reader")
		atsControlWitness(t, p, b, c)
	})
}
