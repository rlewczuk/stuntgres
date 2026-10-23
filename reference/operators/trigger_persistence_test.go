package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCorePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tr_saved")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA tr_saved",
			"CREATE TABLE tr_saved.data(id integer PRIMARY KEY,v integer)",
			"CREATE FUNCTION tr_saved.add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+TG_ARGV[0]::integer; RETURN NEW; END$$",
			"CREATE FUNCTION tr_saved.twice() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v*2; RETURN NEW; END$$",
			"CREATE TRIGGER z BEFORE INSERT ON tr_saved.data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tr_saved.twice()",
			"CREATE TRIGGER \"a ' quoted\" BEFORE INSERT ON tr_saved.data FOR EACH ROW EXECUTE FUNCTION tr_saved.add('3')")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_saved.data VALUES(1,5)", 1))
		q := "INSERT INTO tr_saved.data VALUES($1,5) RETURNING id,v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, triggerRows("SELECT id,v FROM tr_saved.data ORDER BY id", []any{int64(1), int64(16)}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, triggerRows("INSERT INTO tr_saved.data VALUES(99,5) RETURNING id,v", []any{int64(99), int64(16)}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("ROLLBACK", 0))
		}
		parametersReplay(t, p, b, c, "tr_saved", check)
		// The same statement must survive both native Reset/Restore rounds.
		s := triggerRows(q, []any{int64(2), int64(16)})
		s.Args = []any{2}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,v FROM tr_saved.data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		bad := dump.String() + "; CREATE TABLE public.tr_staged_only(x integer); CREATE TRIGGER bad BEFORE INSERT ON tr_saved.data FOR EACH ROW EXECUTE FUNCTION public.tr_missing();"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid late trigger accepted")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: dump.Bytes()}); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS v FROM pg_class WHERE relname='tr_staged_only'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
		s = triggerRows(q, []any{int64(3), int64(16)})
		s.Args = []any{3}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
		for _, id := range []int64{1, 2} {
			if !old.Next() {
				t.Fatal("retained row missing", old.Err())
			}
			var got, v int64
			if err := old.Scan(&got, &v); err != nil || got != id || v != 16 {
				t.Fatal(got, v, err)
			}
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained rows cardinality", old.Err())
		}
	})
}

func TestReferenceTriggerCoreWhenIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tr_when")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA tr_when",
			"CREATE TABLE tr_when.data(id integer PRIMARY KEY,v integer,mark text)",
			"CREATE FUNCTION tr_when.allowed(integer) RETURNS boolean LANGUAGE SQL AS 'SELECT $1>0'",
			"CREATE FUNCTION tr_when.allowed(text) RETURNS boolean LANGUAGE SQL AS 'SELECT false'",
			"CREATE FUNCTION tr_when.touch() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.mark:=NEW.mark||':fired'; RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT ON tr_when.data FOR EACH ROW WHEN(tr_when.allowed(NEW.v)) EXECUTE FUNCTION tr_when.touch()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO tr_when.data VALUES(1,5,'base'),(2,-1,'base')", 2))
		userOperatorExec(t, p, b, c,
			"ALTER TABLE tr_when.data RENAME COLUMN v TO amount",
			"ALTER TABLE tr_when.data RENAME TO renamed",
			"CREATE FUNCTION tr_when.allowed(bigint) RETURNS boolean LANGUAGE SQL AS 'SELECT false'")
		q := "INSERT INTO tr_when.renamed VALUES($1,$2,'base') RETURNING id,amount,mark"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		row := func(query string, want ...[]any) fixture.Step {
			return fixture.Select(query, []string{"id", "amount", "mark"}, []string{"INT4", "INT4", "TEXT"}, want)
		}
		insert := func(conn *sql.Conn, backend fixture.Backend, id, value int64, mark string) {
			s := row(q, []any{id, value, mark})
			s.Args = []any{id, value}
			if conn == c {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, backend.Recorder, s)
			} else {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			}
		}
		check := func(conn *sql.Conn, backend fixture.Backend) {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, row("SELECT id,amount,mark FROM tr_when.renamed ORDER BY id",
				[]any{int64(1), int64(5), "base:fired"}, []any{int64(2), int64(-1), "base"}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("BEGIN", 0))
			insert(conn, backend, 99, 8, "base:fired")
			insert(conn, backend, 98, -8, "base")
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("ROLLBACK", 0))
		}
		// Every unchanged stream is also replayed on PostgreSQL. The original
		// native held handle is exercised inside both native replay rounds.
		parametersReplay(t, p, b, c, "tr_when", check)
		insert(c, b, 3, 7, "base:fired")
		insert(c, b, 4, -7, "base")
		userOperatorExec(t, p, b, c, "DROP FUNCTION tr_when.allowed(text)", "DROP FUNCTION tr_when.allowed(bigint)")
		run(fixture.Error("DROP FUNCTION tr_when.allowed(integer)", "2BP01"))
		insert(c, b, 5, 9, "base:fired")
		userOperatorExec(t, p, b, c,
			"DROP FUNCTION tr_when.allowed(integer) CASCADE",
			"CREATE FUNCTION tr_when.allowed(integer) RETURNS boolean LANGUAGE SQL AS 'SELECT false'")
		insert(c, b, 6, 9, "base")
		userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_when.renamed FOR EACH ROW WHEN(tr_when.allowed(NEW.amount)) EXECUTE FUNCTION tr_when.touch()")
		insert(c, b, 7, 9, "base")
		run(fixture.Error("DROP FUNCTION tr_when.allowed(integer)", "2BP01"))
		run(row("SELECT id,amount,mark FROM tr_when.renamed ORDER BY id",
			[]any{int64(1), int64(5), "base:fired"}, []any{int64(2), int64(-1), "base"},
			[]any{int64(3), int64(7), "base:fired"}, []any{int64(4), int64(-7), "base"},
			[]any{int64(5), int64(9), "base:fired"}, []any{int64(6), int64(9), "base"}, []any{int64(7), int64(9), "base"}))
	})
}
