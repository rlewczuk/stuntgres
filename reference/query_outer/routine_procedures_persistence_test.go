package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func proceduresPersistSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	proceduresMixSetup(t, p, b, c)
	resultsExec(t, p, b, c,
		"CREATE TABLE pc_persist(v integer)",
		"CREATE TYPE pc_persist_pair AS (key integer,val text)",
		"CREATE PROCEDURE pc_persist_write(IN delta integer,OUT answer integer) LANGUAGE SQL AS 'UPDATE pc_persist SET v=v+delta;SELECT v FROM pc_persist'",
		"CREATE PROCEDURE pc_persist_standard(IN delta integer,OUT answer integer) BEGIN ATOMIC UPDATE pc_persist SET v=v+delta;SELECT v FROM pc_persist;END",
		"CREATE PROCEDURE pc_persist_pl(IN x integer,INOUT y integer,OUT note text) LANGUAGE plpgsql AS $$DECLARE stored integer;BEGIN UPDATE pc_persist SET v=v+x RETURNING v INTO stored;y:=y+x;note:='done';END$$",
		"CREATE PROCEDURE pc_persist_default(INOUT n integer DEFAULT 3) LANGUAGE plpgsql AS $$BEGIN RETURN;END$$",
		"CREATE PROCEDURE pc_persist_child(OUT item pc_persist_pair) LANGUAGE SQL AS $$SELECT ROW(4,'pair')::pc_persist_pair$$",
		"CREATE PROCEDURE pc_persist_array(OUT answer integer[],VARIADIC xs integer[] DEFAULT '{}'::integer[]) LANGUAGE SQL AS 'SELECT xs'",
		"CREATE FUNCTION pc_source() RETURNS integer LANGUAGE SQL AS 'SELECT 5'",
		"CREATE PROCEDURE pc_frozen(OUT answer integer) BEGIN ATOMIC SELECT pc_source();END",
		"CREATE PROCEDURE pc_soft(OUT answer integer) LANGUAGE SQL AS 'SELECT pc_source()'",
		"ALTER FUNCTION pc_source() RENAME TO pc_renamed",
		"CREATE FUNCTION pc_source() RETURNS integer LANGUAGE SQL AS 'SELECT 9'")
	resultsExec(t, p, b, c,
		"CREATE PROCEDURE pc_persist_inner(IN delta integer) LANGUAGE SQL AS 'UPDATE pc_persist SET v=v+delta'",
		"CREATE PROCEDURE pc_persist_nested(IN delta integer) LANGUAGE SQL AS 'CALL pc_persist_inner(delta)'")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pc_persist VALUES(24)", 1))
}

// All witnesses leave the same persistent data behind for the next replay round.
func proceduresPersistCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_persist", []string{"v"}, []string{"INT4"}, [][]any{{int64(24)}})
	proceduresMixCheck(t, p, b, c)
	for _, name := range []string{"pc_persist_write", "pc_persist_standard"} {
		q := "CALL " + name + "($1::integer,NULL)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_persist", []string{"v"}, []string{"INT4"}, [][]any{{int64(24)}})
		for _, prepared := range []bool{false, true} {
			s := proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(26)}}, 2)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE pc_persist SET v=24", 1))
		}
		for i, delta := range []int{3, 4} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{[]int64{27, 31}[i]}}, delta))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_persist", []string{"v"}, []string{"INT4"}, [][]any{{int64(31)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE pc_persist SET v=24", 1))
	}
	for _, prepared := range []bool{false, true} {
		step := proceduresStep(b, "CALL pc_persist_nested(2)", []string{}, []string{}, [][]any{})
		if prepared {
			step = fixture.Prepared(step)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_persist", []string{"v"}, []string{"INT4"}, [][]any{{int64(26)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE pc_persist SET v=24", 1))
	}
	for _, prepared := range []bool{false, true} {
		s := proceduresStep(b, "CALL pc_persist_pl(4,10,NULL)", []string{"y", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(14), "done"}})
		if prepared {
			s = fixture.Prepared(s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_persist", []string{"v"}, []string{"INT4"}, [][]any{{int64(28)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE pc_persist SET v=24", 1))
	}
	proceduresHeld(t, p, b, c, "CALL pc_persist_pl($1::integer,$2::integer,NULL)", []string{"y", "note"}, []string{"INT4", "TEXT"}, [][]any{{2, 10}, {3, 20}}, [][][]any{{{int64(12), "done"}}, {{int64(23), "done"}}})
	resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_persist", []string{"v"}, []string{"INT4"}, [][]any{{int64(29)}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE pc_persist SET v=24", 1))
	proceduresBoth(t, p, b, c, "CALL pc_persist_child(NULL)", []string{"item"}, []string{""}, [][]any{{"(4,pair)"}})
	proceduresBoth(t, p, b, c, "CALL pc_persist_array(NULL)", []string{"answer"}, []string{"_INT4"}, [][]any{{"{}"}})
	proceduresBoth(t, p, b, c, "CALL pc_persist_default()", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}})
	proceduresBoth(t, p, b, c, "CALL pc_frozen(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}})
	proceduresBoth(t, p, b, c, "CALL pc_soft(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(9)}})
	resultsBothMetadata(t, p, b, c, "SELECT prokind::text AS kind,proargnames::text AS names,proargmodes::text AS modes,proallargtypes::text AS types,proargtypes::text AS inputs,pronargdefaults::integer AS defaults,prorettype::bigint AS result FROM pg_proc WHERE proname='pc_mix'", []string{"kind", "names", "modes", "types", "inputs", "defaults", "result"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "INT8"}, [][]any{{"p", "{first,x,total,note,bonus}", "{o,i,b,o,i}", "{23,23,20,25,23}", "23 20 23", int64(1), int64(2249)}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, {}, {}})
	proceduresDefaultCountsCheck(t, p, b, c)
	resultsBothMetadata(t, p, b, c, "SELECT pronargdefaults::integer AS defaults FROM pg_proc WHERE proname IN ('pc_persist_array','pc_persist_default') ORDER BY proname", []string{"defaults"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}, []resultsNativeColumn{{}})
	resultsBothMetadata(t, p, b, c, "SELECT p.proallargtypes[1]=t.oid AS child,p.prorettype=2249 AS tuple,l.lanname::text AS language FROM pg_proc p JOIN pg_type t ON t.typname='pc_persist_pair' JOIN pg_language l ON l.oid=p.prolang WHERE p.proname='pc_persist_child'", []string{"child", "tuple", "language"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{true, true, "sql"}}, []resultsNativeColumn{{}, {}, resultsTextMetadata})
	resultsBothMetadata(t, p, b, c, "SELECT p.proargnames::text AS names,p.proargmodes::text AS modes,p.proallargtypes::text AS types,l.lanname::text AS language FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.proname='pc_persist_pl'", []string{"names", "modes", "types", "language"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"{x,y,note}", "{i,b,o}", "{23,23,25}", "plpgsql"}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata})
}

func TestReferenceRoutineProceduresPersistence(t *testing.T) {
	p := fixture.NewScenario(t)
	// Observe shared PG SQL first during oracle discovery, but do not filter native
	// controls: genuine native-emitted SQL must still be replayed on PG unchanged.
	backends := p.Backends()
	for i := len(backends) - 1; i >= 0; i-- {
		b := backends[i]
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			proceduresPersistSetup(t, p, b, c)
			check := func(conn *sql.Conn, backend fixture.Backend) { proceduresPersistCheck(t, p, backend, conn) }
			check(c, b)
			resultsDeferredOutputReplay(t, p, b, c, "pc_restore_bad", true, false)
			resultsDeferredOutputReplay(t, p, b, c, "pc_restore_standard_bad", true, true)
			if b.PG {
				return
			} // Native controls have no PG API counterpart.
			retained, err := c.QueryContext(p.Ctx, "CALL pc_out(NULL)")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			resultsExec(t, p, b, c, "CREATE OR REPLACE PROCEDURE pc_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 8'")
			proceduresBoth(t, p, b, c, "CALL pc_out(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(8)}})
			resultsExec(t, p, b, c, "CREATE OR REPLACE PROCEDURE pc_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 7'")
			var emitted []byte
			for range 2 {
				emitted = resultsNativeReplayRound(t, p, b, c, check)
				if p.Postgres != nil {
					func() {
						pg, err := p.Postgres.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer pg.Close()
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public;GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
							t.Fatal(err)
						}
						if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
							t.Fatal("unchanged procedure dump replay", err)
						}
						if _, err := pg.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION;SET check_function_bodies=true"); err != nil {
							t.Fatal(err)
						}
						check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
					}()
				}
			}
			proceduresMetadata(b, []string{"answer"}, []string{"INT4"})(t, retained)
			if !retained.Next() {
				t.Fatal("lost detached CALL row", retained.Err())
			}
			var answer int64
			if err := retained.Scan(&answer); err != nil || answer != 7 {
				t.Fatal(answer, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
			retained.Close()
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			raw := sql.OpenDB(connector)
			defer raw.Close()
			recorded, rec := fixture.RecordedNativePool(t, connector)
			fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE TYPE pc_destination_pressure AS (x integer)", 0))
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			clone, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer clone.Close()
			check(clone, fixture.Backend{Recorder: rec})
			fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("UPDATE pc_persist SET v=99", 1))
			resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_persist", []string{"v"}, []string{"INT4"}, [][]any{{int64(24)}})
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			// Private false replay preserves analyzed standard actions while
			// deferring the validator's output-shape check to actual CALL.
			bad := before.String() + ";CREATE TABLE pc_restore_prefix(v integer);CREATE PROCEDURE pc_restore_bad(OUT a integer,OUT b integer) BEGIN ATOMIC SELECT 1; END;"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err != nil {
				t.Fatal("deferred standard procedure replay", err)
			}
			want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return record")
			want.Detail, want.Context = "Final statement returns too few columns.", `SQL function "pc_restore_bad" statement 1`
			resultsError(t, p, c, "CALL pc_restore_bad(NULL,NULL)", true, "execute", want)
			check(c, b)
			proceduresCount(t, p, b, c, "pg_class WHERE relname='pc_restore_prefix'", 1)
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_restore_bad'", 1)
			resultsExec(t, p, b, c, "DROP PROCEDURE pc_restore_bad()", "DROP TABLE pc_restore_prefix")
			proceduresCount(t, p, b, c, "pg_class WHERE relname='pc_restore_prefix'", 0)
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_restore_bad'", 0)
			// Top-level CALL is not emitted by Dump and remains outside Restore
			// admission. This is not a body-invocation diagnostic.
			bad = before.String() + ";CREATE TABLE pc_restore_prefix(v integer);CREATE PROCEDURE pc_restore_bad(OUT a integer,OUT b integer) LANGUAGE SQL AS 'SELECT 1';CALL pc_restore_bad(NULL,NULL);"
			admission := fixture.ControlsReferenceDiagnostic("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &admission)
			check(c, b)
			proceduresCount(t, p, b, c, "pg_class WHERE relname='pc_restore_prefix'", 0)
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_restore_bad'", 0)
			failure := errors.New("procedure reader failure")
			if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader("CREATE TABLE pc_reader_prefix(v integer);"), resultsFailReader{failure})); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			check(c, b)
			proceduresCount(t, p, b, c, "pg_class WHERE relname='pc_reader_prefix'", 0)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(c, b)
			if err := stuntgres.Dump(p.Ctx, p.Native, proceduresFailWriter{}); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal("Dump writer failure", err)
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Error("failed controls changed procedure catalog/data")
			}
		})
	}
}

type proceduresFailWriter struct{}

func (proceduresFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
