package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Coordinates are independently specified body coordinates, not extracted from
// the error under test. PostgreSQL reports inlining coordinates internally.
func quotedReplayDiagnostic(pg bool, name, body, position, code, message, detail, hint string, heldStatement bool) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail, d.Hint = detail, hint
	d.Context = "SQL function \"" + name + "\" statement 1"
	if position != "" {
		if pg {
			d.InternalPosition, d.InternalQuery = position, body
			if !heldStatement {
				d.Context = "SQL function \"" + name + "\" during inlining"
			}
		} else {
			d.Position = position
		}
	}
	return d
}

func quotedReplayFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held *sql.Stmt, q, name, body, position, code, message, detail, hint string) {
	t.Helper()
	for _, route := range []string{"direct", "fresh", "held"} {
		if route == "held" && held == nil {
			continue
		}
		statement := route == "held" && (code == "42703" || name == "qr_test" && code == "42P01")
		want := quotedReplayDiagnostic(b.PG, name, body, position, code, message, detail, hint, statement)
		s := fixture.ErrorDiagnostic(q, want)
		s.SelectRows, s.ErrorStage = true, "execute"
		switch route {
		case "direct":
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		case "fresh":
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		case "held":
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
		}
	}
}

func TestReferenceRoutineQuotedReplayDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, setup, body, invalidate, repair, code, message, detail, hint string
	}{
		{"relation", "CREATE TABLE qr_source(a integer); INSERT INTO qr_source VALUES(7)", "SELECT a FROM qr_source", "DROP TABLE qr_source", "CREATE TABLE qr_source(a integer); INSERT INTO qr_source VALUES(7)", "42P01", "relation \"qr_source\" does not exist", "", ""},
		{"column", "CREATE TABLE qr_source(a integer); INSERT INTO qr_source VALUES(7)", "SELECT a FROM qr_source", "ALTER TABLE qr_source RENAME COLUMN a TO renamed_a", "ALTER TABLE qr_source RENAME COLUMN renamed_a TO a", "42703", "column \"a\" does not exist", "", ""},
		{"call", "CREATE FUNCTION qr_callee(integer) RETURNS integer RETURN $1", "SELECT qr_callee(7)", "DROP FUNCTION qr_callee(integer)", "CREATE FUNCTION qr_callee(integer) RETURNS integer RETURN $1", "42883", "function qr_callee(integer) does not exist", "", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"operator", "CREATE OPERATOR public.### (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)", "SELECT 3 OPERATOR(public.###) 4", "DROP OPERATOR public.### (integer,integer)", "CREATE OPERATOR public.### (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)", "42883", "operator does not exist: integer public.### integer", "", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		{"ambiguity", "CREATE FUNCTION qr_callee(integer) RETURNS integer RETURN 7", "SELECT qr_callee(NULL)", "CREATE FUNCTION qr_callee(bigint) RETURNS integer RETURN 7", "DROP FUNCTION qr_callee(bigint)", "42725", "function qr_callee(unknown) is not unique", "", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"result", "CREATE TABLE qr_source(a integer); INSERT INTO qr_source VALUES(7)", "SELECT a FROM qr_source", "DROP TABLE qr_source; CREATE TABLE qr_source(a text)", "DROP TABLE qr_source; CREATE TABLE qr_source(a integer); INSERT INTO qr_source VALUES(7)", "42P13", "return type mismatch in function declared to return integer", "Actual return type is text.", ""},
		{"width", "CREATE TABLE qr_source(a integer); INSERT INTO qr_source VALUES(7)", "SELECT * FROM qr_source", "ALTER TABLE qr_source ADD COLUMN b integer", "ALTER TABLE qr_source DROP COLUMN b", "42P13", "return type mismatch in function declared to return integer", "Final statement must return exactly one column.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if _, err := c.ExecContext(p.Ctx, tc.setup); err != nil {
					t.Fatal(err)
				}
				routineExec(t, p, b, c, "CREATE FUNCTION qr_test() RETURNS integer LANGUAGE SQL AS $$"+tc.body+"$$")
				q := "SELECT qr_test() AS v"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				routineValue(t, p, b, c, q, "v", "INT4", int64(7))
				routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
				if _, err := c.ExecContext(p.Ctx, tc.invalidate); err != nil {
					t.Fatal(err)
				}
				position := map[string]string{"relation": "15", "column": "8", "call": "8", "operator": "10", "ambiguity": "8"}[tc.name]
				failedHeld := held
				if tc.name == "ambiguity" && b.PG {
					// Adding an overload does not invalidate the warmed PG plan.
					routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
					failedHeld = nil
				}
				quotedReplayFailure(t, p, b, c, failedHeld, q, "qr_test", tc.body, position, tc.code, tc.message, tc.detail, tc.hint)
				if tc.name == "ambiguity" {
					routineExec(t, p, b, c, "DISCARD PLANS")
					// Explicit invalidation makes this same warmed handle analyze
					// the ambiguous body again; ordinary retained-plan success
					// above remains a separate PostgreSQL outcome.
					want := quotedReplayDiagnostic(b.PG, "qr_test", tc.body, position, tc.code, tc.message, tc.detail, tc.hint, false)
					s := fixture.ErrorDiagnostic(q, want)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
				}
				if !b.PG {
					// A private definition-only replay must store exactly the same
					// stale source; no history/classification distinguishes it.
					var source string
					stream := "SET check_function_bodies = false; CREATE FUNCTION qr_test() RETURNS integer LANGUAGE SQL AS $$" + tc.body + "$$;"
					// Keep current non-routine objects for the later recovery phase.
					// Dump/Restore persistence is covered by the separate family;
					// this isolated target proves genuinely unresolved storage.
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/unresolved")
					if err != nil {
						t.Fatal(err)
					}
					db := sql.OpenDB(connector)
					defer db.Close()
					if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(stream)); err != nil {
						t.Fatal("definition-only replay", err)
					}
					if err := db.QueryRowContext(p.Ctx, "SELECT prosrc FROM pg_proc WHERE oid='qr_test()'::regprocedure").Scan(&source); err != nil || source != tc.body {
						t.Fatal(source, err)
					}
				}
				if _, err := c.ExecContext(p.Ctx, tc.repair); err != nil {
					t.Fatal(err)
				}
				routineValue(t, p, b, c, q, "v", "INT4", int64(7))
				routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
			})
		})
	}
	t.Run("lookup ACL", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"qr_reader"})
		fixture.SchemaReferenceCleanup(t, "qr_private", "qr_owned")
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE ROLE qr_reader", "CREATE SCHEMA qr_private", "CREATE SCHEMA qr_owned AUTHORIZATION qr_reader", "CREATE FUNCTION qr_private.callee(integer) RETURNS integer RETURN $1", "GRANT USAGE ON SCHEMA qr_private TO qr_reader", "CREATE FUNCTION qr_owned.caller() RETURNS integer LANGUAGE SQL AS $$SELECT qr_private.callee(7)$$", "SET SESSION AUTHORIZATION qr_reader")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			q := "SELECT qr_owned.caller() AS v"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			routineValue(t, p, b, c, q, "v", "INT4", int64(7))
			routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE USAGE ON SCHEMA qr_private FROM qr_reader", "SET SESSION AUTHORIZATION qr_reader")
			quotedReplayFailure(t, p, b, c, held, q, "caller", "SELECT qr_private.callee(7)", "8", "42501", "permission denied for schema qr_private", "", "")
			routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT USAGE ON SCHEMA qr_private TO qr_reader", "SET SESSION AUTHORIZATION qr_reader")
			routineValue(t, p, b, c, q, "v", "INT4", int64(7))
			routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
		})
	})
	t.Run("no source history", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			stream := "SET check_function_bodies = false; CREATE FUNCTION qr_unresolved() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM qr_absent$$;"
			if b.PG {
				if _, err := c.ExecContext(p.Ctx, stream); err != nil {
					t.Fatal(err)
				}
				routineExec(t, p, b, c, "SET check_function_bodies=true")
			} else if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			q := "SELECT qr_unresolved() AS v"
			quotedReplayFailure(t, p, b, c, nil, q, "qr_unresolved", "SELECT x FROM qr_absent", "15", "42P01", "relation \"qr_absent\" does not exist", "", "")
			// Prepare while invalid when the backend permits it; a genuinely
			// held, previously valid handle is covered in every lawful-DDL case.
			routineExec(t, p, b, c, "CREATE TABLE qr_absent(x integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qr_absent VALUES(7)", 1))
			routineValue(t, p, b, c, q, "v", "INT4", int64(7))
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			routineExec(t, p, b, c, "DROP TABLE qr_absent")
			quotedReplayFailure(t, p, b, c, held, q, "qr_unresolved", "SELECT x FROM qr_absent", "15", "42P01", "relation \"qr_absent\" does not exist", "", "")
			routineExec(t, p, b, c, "CREATE TABLE qr_absent(x integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qr_absent VALUES(7)", 1))
			routineValue(t, p, b, c, q, "v", "INT4", int64(7))
			routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
		})
	})
}
