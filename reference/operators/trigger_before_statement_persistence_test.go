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

func beforeStatementSavedSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE SCHEMA bst_saved",
		"CREATE TABLE bst_saved.data(id integer PRIMARY KEY,v integer)",
		"CREATE TABLE bst_saved.audit(seq integer GENERATED ALWAYS AS IDENTITY,name text,op text,level text,n bigint,arg text)",
		"CREATE FUNCTION bst_saved.log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_saved.audit(name,op,level,n,arg) VALUES(TG_NAME,TG_OP,TG_LEVEL,(SELECT count(*) FROM bst_saved.data),TG_ARGV[0]); RETURN NULL; END$$",
		"CREATE FUNCTION bst_saved.row_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1; RETURN NEW; END$$",
		"CREATE TRIGGER row_add BEFORE INSERT ON bst_saved.data FOR EACH ROW EXECUTE FUNCTION bst_saved.row_add()",
		"CREATE TRIGGER z BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON bst_saved.data EXECUTE FUNCTION bst_saved.log('x''y')",
		"CREATE TRIGGER a BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON bst_saved.data FOR EACH STATEMENT WHEN(true) EXECUTE FUNCTION bst_saved.log('')",
		"CREATE TRIGGER \"of ' quoted\" BEFORE UPDATE OF v ON bst_saved.data WHEN(false) EXECUTE FUNCTION bst_saved.log('not reached')",
		"ALTER TABLE bst_saved.data ENABLE ALWAYS TRIGGER a")
}
func beforeStatementSavedLog(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT name,op,level,n,arg FROM bst_saved.audit ORDER BY seq", []string{"name", "op", "level", "n", "arg"}, []string{"TEXT", "TEXT", "TEXT", "INT8", "TEXT"}, rows)
}
func beforeStatementSavedPair(op string, n int64) [][]any {
	return [][]any{{"a", op, "STATEMENT", n, ""}, {"z", op, "STATEMENT", n, "x'y"}}
}
func TestReferenceTriggerBeforeStatementPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "bst_saved")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		beforeStatementSavedSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bst_saved.data VALUES(1,10),(2,20)", 2))
		baseline := beforeStatementSavedPair("INSERT", 0)
		check := func(conn *sql.Conn, backend fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(triggerRows("SELECT id,v FROM bst_saved.data ORDER BY id", []any{int64(1), int64(11)}, []any{int64(2), int64(21)}))
			run(beforeStatementSavedLog(baseline...))
			run(fixture.Select("SELECT tgname,tgtype::integer AS bits,tgenabled FROM pg_trigger WHERE tgrelid='bst_saved.data'::regclass ORDER BY tgname", []string{"tgname", "bits", "tgenabled"}, []string{"NAME", "INT4", "CHAR"}, [][]any{{"a", int64(62), "A"}, {"of ' quoted", int64(18), "O"}, {"row_add", int64(7), "O"}, {"z", int64(62), "O"}}))
			run(fixture.Select("SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='bst_saved.data'::regclass AND tgname='of '' quoted'", []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE TRIGGER "of ' quoted" BEFORE UPDATE OF v ON bst_saved.data FOR EACH STATEMENT WHEN (false) EXECUTE FUNCTION bst_saved.log('not reached')`}}))
			run(fixture.Exec("BEGIN", 0))
			q := "INSERT INTO bst_saved.data VALUES($1,$2) RETURNING id,v"
			h, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			for _, id := range []int{3, 4} {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: h, SQL: q}, backend.Recorder, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(id), int64(31)}}, id, 30))
			}
			run(fixture.Exec("UPDATE bst_saved.data SET v=v WHERE id=1", 1))
			run(fixture.Exec("DELETE FROM bst_saved.data WHERE id=2", 1))
			run(fixture.Exec("TRUNCATE bst_saved.data", 0))
			want := append([][]any(nil), baseline...)
			for _, event := range []struct {
				op string
				n  int64
			}{{"INSERT", 2}, {"INSERT", 3}, {"UPDATE", 4}, {"DELETE", 4}, {"TRUNCATE", 3}} {
				want = append(want, beforeStatementSavedPair(event.op, event.n)...)
			}
			run(beforeStatementSavedLog(want...))
			run(fixture.Exec("ROLLBACK", 0))
			run(beforeStatementSavedLog(baseline...))
		}
		parametersReplay(t, p, b, c, "bst_saved", check)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		cb := fixture.Backend{Recorder: rec}
		check(clone, cb)
		fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("CREATE TABLE sentinel(id integer)", 0))
		fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("INSERT INTO sentinel VALUES(73)", 1))
		old, err := clone.QueryContext(p.Ctx, "SELECT id FROM sentinel")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		for _, restore := range []func() error{
			func() error {
				return stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump.String()+"; CREATE TRIGGER bad BEFORE INSERT ON bst_saved.data EXECUTE FUNCTION bst_saved.missing();"))
			},
			func() error { return stuntgres.Restore(p.Ctx, raw, &plpgsqlFailReader{data: dump.Bytes()}) },
			func() error {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				return stuntgres.Restore(ctx, raw, bytes.NewReader(dump.Bytes()))
			},
		} {
			if err := restore(); err == nil {
				t.Fatal("invalid restore succeeded")
			}
			fixture.RunStep(t, p.Ctx, clone, rec, beforeStatementCount("sentinel", 1))
			check(clone, cb)
		}
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var id int64
		if err := old.Scan(&id); err != nil || id != 73 {
			t.Fatal(id, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
	})
}
func TestReferenceTriggerBeforeStatementPreparedLifetime(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		beforeStatementSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TRIGGER s BEFORE INSERT ON bst EXECUTE FUNCTION bst_log('old')")
		q := "INSERT INTO bst(id,v) VALUES($1,$2)"
		h, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		run(beforeStatementCount("bst_log", 0))
		exec := func(id int) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: h, SQL: q}, b.Recorder, fixture.Exec(q, 1, id, id*10))
		}
		exec(1)
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER s BEFORE INSERT ON bst WHEN(true) EXECUTE FUNCTION bst_log('new')")
		exec(2)
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION bst_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_log(name,arg0) VALUES(TG_NAME,'replacement'); RETURN NEW; END$$")
		exec(3)
		run(fixture.Select("SELECT arg0 FROM bst_log ORDER BY seq", []string{"arg0"}, []string{"TEXT"}, [][]any{{"old"}, {"new"}, {"replacement"}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			exec(4)
			if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: dump.Bytes()}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			exec(5)
			run(beforeStatementCount("bst_log", 5))
		}
	})
}
func TestReferenceTriggerBeforeStatementFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "bst_saved")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		beforeStatementSavedSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bst_saved.data VALUES(1,10),(2,20)", 2))
		userOperatorExec(t, p, b, c, "TRUNCATE bst_saved.audit")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "UPDATE bst_saved.data SET v=v WHERE id=$1"
		h, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		run(beforeStatementCount("bst_saved.audit", 0))
		for _, id := range []int{998, 999} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: h, SQL: q}, b.Recorder, fixture.Exec(q, 0, id))
		}
		want := append(beforeStatementSavedPair("UPDATE", 2), beforeStatementSavedPair("UPDATE", 2)...)
		s := triggerRows("INSERT INTO bst_saved.data VALUES(3,30),(4,40) RETURNING id,v", []any{int64(3), int64(31)}, []any{int64(4), int64(41)})
		s.Bag = true
		run(s)
		want = append(want, beforeStatementSavedPair("INSERT", 2)...)
		run(fixture.Exec("INSERT INTO bst_saved.data VALUES(3,50) ON CONFLICT(id) DO UPDATE SET v=excluded.v", 1))
		want = append(want, beforeStatementSavedPair("INSERT", 4)...)
		want = append(want, beforeStatementSavedPair("UPDATE", 4)...)
		run(beforeStatementSavedLog(want...))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION bst_saved.fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$", "CREATE TRIGGER zz BEFORE INSERT ON bst_saved.data EXECUTE FUNCTION bst_saved.fail()")
		d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function bst_saved.fail() line 1 at PERFORM"
		run(fixture.ErrorDiagnostic("INSERT INTO bst_saved.data VALUES(5,50)", d))
		run(beforeStatementCount("bst_saved.data", 4))
		run(beforeStatementSavedLog(want...))
		userOperatorExec(t, p, b, c, "DROP TRIGGER zz ON bst_saved.data")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, beforeStatementSavedLog(want...))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO bst_saved.data VALUES(5,50)", 1))
			expected := append(append([][]any(nil), want...), beforeStatementSavedPair("INSERT", 4)...)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, beforeStatementSavedLog(expected...))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("ROLLBACK", 0))
		}
		parametersReplay(t, p, b, c, "bst_saved", check)
	})
}
