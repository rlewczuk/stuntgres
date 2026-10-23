package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLConditionalAtomicity(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE conditional_atomic(v integer)",
			"CREATE FUNCTION conditional_atomic_value(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN CASE x WHEN 1,2 THEN RETURN x; END CASE; END$$")
		want := plpgsqlRuntimeDiagnostic("20000", "case not found", "conditional_atomic_value(integer)", "CASE")
		want.Hint = "CASE statement is missing ELSE part."
		conditionalDiagnostic(t, p, c, "INSERT INTO conditional_atomic VALUES(conditional_atomic_value(1)),(conditional_atomic_value(0))", false, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM conditional_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, nil))
		q := "SELECT conditional_atomic_value($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, input := range []int{1, 0, 2, 0, 1} {
			if input == 0 {
				rows, err := stmt.QueryContext(p.Ctx, input)
				if rows != nil {
					rows.Close()
					t.Fatal("CASE error exposed rows")
				}
				if err == nil {
					t.Fatal("unmatched CASE succeeded")
				}
				got := fixture.ExtractDiagnostic(t, err)
				if got != want {
					t.Errorf("held diagnostic got=%#v want=%#v", got, want)
				}
				continue
			}
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(input)}}, input)
			s.CheckMetadata = parametersMetadata(b, "v", "INT4")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO conditional_atomic VALUES(conditional_atomic_value(1)),(conditional_atomic_value(2))", 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM conditional_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, 1)
			if rows != nil {
				rows.Close()
				t.Fatal("pre-canceled invocation exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled invocation: %v", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatalf("ordinary cancellation acquired SQLSTATE: %v", err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}, 1))
		}
	})
}

func TestReferencePLpgSQLConditionalPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "conditional_saved")
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA conditional_saved",
			"CREATE SEQUENCE conditional_saved.search_seq",
			"CREATE FUNCTION conditional_saved.pick(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE r integer := 7; BEGIN IF x IS NULL THEN CASE WHEN NULL::boolean THEN r := 90; ELSE END CASE; RETURN r; ELSE CASE x WHEN 1,2 THEN IF 'yes'::text THEN RETURN 12; END IF; ELSE RETURN 30; END CASE; END IF; RETURN r; END$$",
			"CREATE FUNCTION conditional_saved.positive(x integer) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN IF x>0 THEN RETURN true; ELSE RETURN false; END IF; END$$",
			"CREATE FUNCTION conditional_saved.search() RETURNS integer LANGUAGE plpgsql AS $$BEGIN CASE nextval('conditional_saved.search_seq') WHEN 1 THEN RETURN 10; ELSE RETURN 30; END CASE; END$$",
			"CREATE VIEW conditional_saved.captured AS SELECT conditional_saved.pick(2) AS v",
			"CREATE TABLE conditional_saved.data(id integer,v integer DEFAULT conditional_saved.pick(NULL),CONSTRAINT conditional_positive CHECK(conditional_saved.positive(v)))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO conditional_saved.data(id) VALUES(1)", 1))
		// Establish one sequence call. Compiling/preparing/dumping/restoring search()
		// must not execute its search expression; last_value must stay exactly1.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('conditional_saved.search_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		search, err := c.PrepareContext(p.Ctx, "SELECT conditional_saved.search() AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer search.Close()
		q := "SELECT conditional_saved.pick($1) AS v"
		original, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM conditional_saved.captured", "INT4", int64(12))
			// This stored table scalar has known native nullability, unlike function outputs.
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM conditional_saved.data WHERE id=1", "INT4", int64(7))
			plpgsqlValue(t, p, backend, conn, "SELECT last_value AS v FROM conditional_saved.search_seq", "INT8", int64(1))
			parametersValue(t, p, backend, conn, q, "v", "INT4", int64(7), nil)
			parametersValue(t, p, backend, conn, q, "v", "INT4", int64(30), 3)
			parametersHeld(t, p, backend, conn, q, "INT4", [][]any{{2}, {nil}, {3}, {1}}, []any{int64(12), int64(7), int64(30), int64(12)})
			want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "data" violates check constraint "conditional_positive"`)
			want.Detail = "Failing row contains (2, -2)."
			want.Schema, want.Table, want.Constraint = "conditional_saved", "data", "conditional_positive"
			conditionalDiagnostic(t, p, conn, "INSERT INTO conditional_saved.data VALUES(2,-2)", false, want)
			if conn == c && backend.PG == b.PG {
				for _, tc := range []struct {
					input any
					value int64
				}{{2, 12}, {nil, 7}, {3, 30}, {1, 12}} {
					s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{tc.value}}, tc.input)
					s.CheckMetadata = parametersMetadata(backend, "v", "INT4")
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: original, SQL: q}, backend.Recorder, s)
				}
			}
		}
		// The unchanged stream from both native replay rounds is also consumed on
		// pinned PostgreSQL by the existing common helper.
		parametersReplay(t, p, b, c, "conditional_saved", check)
	})
}
