package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func routineDropCallerSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	routineExec(t, p, b, c, "CREATE FUNCTION rd_callee(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$", "CREATE FUNCTION rd_caller() RETURNS integer LANGUAGE SQL AS $$SELECT rd_callee(4)$$", "CREATE VIEW rd_caller_view AS SELECT rd_caller() AS v")
}

func routineDropCallerMissing(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, initializing bool) {
	t.Helper()
	q := "SELECT rd_caller() AS v"
	d := fixture.ControlsReferenceDiagnostic("42883", "function rd_callee(integer) does not exist")
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	d.Context = "SQL function \"rd_caller\" statement 1"
	// Reached reanalysis retains its statement frame; first-use validation uses
	// the independently specified inlining coordinates on either backend.
	d.Position = "8"
	pg := d
	pg.Position = ""
	pg.Context = "SQL function \"rd_caller\" during inlining"
	pg.InternalQuery = "SELECT rd_callee(4)"
	pg.InternalPosition = "8"
	if initializing {
		d = pg
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d)
		s.PostgresDiagnostic = &pg
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceRoutineDropQuotedBody(t *testing.T) {
	for _, mode := range []string{"RESTRICT", "CASCADE"} {
		t.Run(mode, func(t *testing.T) {
			routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
				routineDropCallerSetup(t, p, b, c)
				q := "SELECT rd_caller() AS v"
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				routineValue(t, p, b, c, q, "v", "INT4", int64(5))
				routineExec(t, p, b, c, "DROP FUNCTION rd_callee(integer) "+mode)
				routineDropNotices(t, n)
				routineDropCallerMissing(t, p, b, c, false)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_class WHERE relname='rd_caller_view'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				routineExec(t, p, b, c, "CREATE FUNCTION rd_callee(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+10$$")
				routineValue(t, p, b, c, q, "v", "INT4", int64(14))
				routineDropHeld(t, p, b, c, s, q, "INT4", int64(14))
				routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM rd_caller_view", "v", "INT4", int64(14), true, true)
			})
		})
	}
}

func routineDropRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		return
	}
	routineExec(t, p, b, c, "CREATE ROLE rd_replay_denied")
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("dangling quoted-body Restore", err)
	}
	routineDropDependencySurvivors(t, p, b, c)
	routineDropCallerMissing(t, p, b, c, true)
	if p.Postgres != nil {
		pg, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("exact emitted SQL replay", err)
		}
		pb := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
		routineDropDependencySurvivors(t, p, pb, pg)
		routineDropCallerMissing(t, p, pb, pg, true)
		routineExec(t, p, pb, pg, "CREATE FUNCTION rd_callee(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+10$$", "SET check_function_bodies = true")
		routineValue(t, p, pb, pg, "SELECT rd_caller() AS v", "v", "INT4", int64(14))
		// The ordinary PG phase below starts from an empty scenario schema.
		if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	// Every quoted source can be reconstructed without validation; actual
	// invocation still enforces complete syntax, closed formals and semantics.
	for _, body := range []string{
		"SELECT rd_missing(1), $1",
		"SELECT rd_missing(1), $2",
		"SELECT 1 +",
		"SELECT 'x'::text",
		"SELECT x FROM rd_missing_table",
		"SELECT 1 OPERATOR(public.###) 2",
	} {
		before := dump.String()
		bad := "SET check_function_bodies = false; CREATE FUNCTION public.rd_bad_replay() RETURNS integer LANGUAGE SQL AS $$" + body + "$$;"
		routineDropStoredReplay(t, p, bad, body)
		bad += " CREATE TABLE rd_reached(v integer DEFAULT public.rd_bad_replay()); INSERT INTO rd_reached DEFAULT VALUES;"
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
		want := routineDropReplayBodyDiagnostic(body, bad)
		position := ""
		switch body {
		case "SELECT rd_missing(1), $1":
			position = fixture.Position(strings.Index(body, "$1") + 1)
		case "SELECT rd_missing(1), $2":
			position = fixture.Position(strings.Index(body, "$2") + 1)
		case "SELECT 1 +":
			position = fixture.Position(len(body) + 1)
		case "SELECT x FROM rd_missing_table":
			position = "15"
		case "SELECT 1 OPERATOR(public.###) 2":
			position = "10"
		}
		want.Position = ""
		want.InternalPosition, want.InternalQuery = position, ""
		if position != "" {
			want.InternalQuery = body
		}
		want.Context = "SQL function \"rd_bad_replay\" during inlining"
		fixture.AssertDiagnostic(t, err, &want)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != before {
			t.Fatal("failed replay published a catalog change", body)
		}
	}
	// Lookup ACLs and ambiguous calls are invocation authority, not evidence
	// that a quoted declaration cannot be stored.
	for _, tc := range []struct {
		sql    string
		denied bool
	}{
		{"CREATE SCHEMA rd_private_source; CREATE SCHEMA rd_private_schema AUTHORIZATION rd_replay_denied; SET SESSION AUTHORIZATION rd_replay_denied; CREATE FUNCTION rd_private_schema.rd_denied() RETURNS integer LANGUAGE SQL AS $$SELECT rd_private_source.rd_missing(1)$$; RESET SESSION AUTHORIZATION;", true},
		{"CREATE FUNCTION public.rd_amb(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$; CREATE FUNCTION public.rd_amb(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$; CREATE FUNCTION public.rd_amb_caller() RETURNS integer LANGUAGE SQL AS $$SELECT public.rd_amb(NULL)$$;", false},
	} {
		body := "SELECT public.rd_amb(NULL)"
		if tc.denied {
			body = "SELECT rd_private_source.rd_missing(1)"
		}
		routineDropStoredReplay(t, p, "SET check_function_bodies = false; "+tc.sql, body)
		invoke := "CREATE TABLE public.rd_reached(v integer DEFAULT public.rd_amb_caller()); INSERT INTO public.rd_reached DEFAULT VALUES;"
		if tc.denied {
			invoke = "SET SESSION AUTHORIZATION rd_replay_denied; CREATE TABLE rd_private_schema.rd_reached(v integer DEFAULT rd_private_schema.rd_denied()); INSERT INTO rd_private_schema.rd_reached DEFAULT VALUES; RESET SESSION AUTHORIZATION;"
		}
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("SET check_function_bodies = false; "+tc.sql+invoke))
		want := routineDropReplayLookupDiagnostic(tc.denied)
		want.InternalPosition, want.InternalQuery, want.Position = want.Position, body, ""
		want.Context = "SQL function \"rd_amb_caller\" during inlining"
		if tc.denied {
			want.Context = "SQL function \"rd_denied\" during inlining"
		}
		fixture.AssertDiagnostic(t, err, &want)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed authorized/ambiguous replay published a catalog change")
		}
	}
}

// Native controls have no PostgreSQL equivalent. The shared ordinary CREATE
// inventory and lawful-DDL invocation family specify their public SQL phases;
// this independent destination isolates definition admission from execution.
func routineDropStoredReplay(t *testing.T, p *fixture.Pair, stream, source string) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name()+"/stored-definition")
	if err != nil { t.Fatal(err) }
	db := sql.OpenDB(connector)
	defer db.Close()
	if _, err := db.ExecContext(p.Ctx, "CREATE ROLE rd_replay_denied"); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(stream)); err != nil { t.Fatal("definition-only replay", source, err) }
	var count int64
	if err := db.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_proc WHERE prosrc=$1::text", source).Scan(&count); err != nil || count != 1 {
		t.Fatal("stored quoted source changed", count, err)
	}
}

func TestReferenceRoutineDropPersistence(t *testing.T) {
	routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
		routineDropDependencySetup(t, p, b, c)
		routineDropCallerSetup(t, p, b, c)
		routineExec(t, p, b, c, "DROP FUNCTION rd_dep(integer) CASCADE", "DROP FUNCTION rd_callee(integer)")
		routineDropNotices(t, n, routineDropDependencyCascadeNotice())
		routineDropDependencySurvivors(t, p, b, c)
		routineDropCallerMissing(t, p, b, c, true)
		routineDropRoundtrip(t, p, b, c)
		routineExec(t, p, b, c, "CREATE FUNCTION rd_callee(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+10$$")
		routineValue(t, p, b, c, "SELECT rd_caller() AS v", "v", "INT4", int64(14))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rd_data DEFAULT VALUES", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT x FROM rd_data ORDER BY x NULLS LAST", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
	})
}

func TestReferenceRoutineDropFeatureSmoke(t *testing.T) {
	routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
		routineDropDependencySetup(t, p, b, c)
		routineDropCallerSetup(t, p, b, c)
		q := "SELECT rd_dep('x'::text) AS v"
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		routineValue(t, p, b, c, "SELECT rd_dep(4) AS v", "v", "INT4", int64(5))
		routineDropError(t, p, b, c, "DROP FUNCTION rd_dep(integer) RESTRICT", routineDropDependencyError())
		routineDropError(t, p, b, c, "DROP PROCEDURE rd_dep(text)", fixture.ControlsReferenceDiagnostic("42809", "rd_dep(text) is not a procedure"))
		routineExec(t, p, b, c, "DROP FUNCTION rd_dep(integer) CASCADE", "DROP FUNCTION IF EXISTS rd_missing()", "DROP ROUTINE rd_callee(integer)")
		routineDropNotices(t, n, routineDropDependencyCascadeNotice(), routineDropNotice("function rd_missing() does not exist, skipping"))
		routineDropDependencySurvivors(t, p, b, c)
		routineDropHeld(t, p, b, c, s, q, "INT8", int64(104))
		routineDropCallerMissing(t, p, b, c, true)
		routineDropRoundtrip(t, p, b, c)
		routineExec(t, p, b, c, "CREATE FUNCTION rd_callee(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+10$$")
		routineValue(t, p, b, c, "SELECT rd_caller() AS v", "v", "INT4", int64(14))
	})
}
