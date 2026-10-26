package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const quotedReplayEmptyDetail = "Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING."

func quotedReplayEmptyDiagnostic(pg bool) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
	d.Detail = quotedReplayEmptyDetail
	// Native uses its ordinary runtime frame, including pre-command failures.
	// PostgreSQL's independently observed no-command error is during startup.
	d.Context = `SQL function "qr_empty" statement 1`
	if pg {
		d.Context = `SQL function "qr_empty" during startup`
	}
	return d
}

func quotedReplayEmptyFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held *sql.Stmt, q, route string) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, quotedReplayEmptyDiagnostic(b.PG))
	s.SelectRows, s.ErrorStage = route != "default INSERT", "execute"
	switch route {
	case "direct", "default INSERT":
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	case "fresh":
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	case "held", "held again":
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
	default:
		t.Fatal("unspecified empty-body route", route)
	}
}

func TestReferenceRoutineQuotedReplayEmptyBodies(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"comment only", "-- no executable statements\n/* retain this source */"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				declarations := "CREATE FUNCTION qr_empty() RETURNS integer LANGUAGE SQL AS $$" + tc.body + "$$; CREATE FUNCTION qr_empty_void() RETURNS void LANGUAGE SQL AS $$" + tc.body + "$$;"
				stream := "SET check_function_bodies = false; " + declarations
				if b.PG {
					if _, err := c.ExecContext(p.Ctx, stream); err != nil {
						t.Fatal("deferred empty declaration storage", err)
					}
					defer c.ExecContext(p.Ctx, "SET check_function_bodies=true")
					routineExec(t, p, b, c, "SET check_function_bodies=true")
				} else if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream)); err != nil {
					t.Fatal("deferred empty declaration storage", err)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
					"SELECT proname,prosrc,prokind,pronargs::integer,prorettype='integer'::regtype AS returns_integer FROM pg_proc WHERE oid IN ('qr_empty()'::regprocedure,'qr_empty_void()'::regprocedure) ORDER BY proname",
					[]string{"proname", "prosrc", "prokind", "pronargs", "returns_integer"}, []string{"NAME", "TEXT", "CHAR", "INT4", "BOOL"},
					[][]any{{"qr_empty", tc.body, "f", int64(0), true}, {"qr_empty_void", tc.body, "f", int64(0), false}}))

				// Empty and comment-only VOID definitions remain executable, both
				// through private replay and ordinary enabled CREATE.
				routineExec(t, p, b, c, "CREATE FUNCTION qr_public_void() RETURNS void LANGUAGE SQL AS $$"+tc.body+"$$")
				for _, name := range []string{"qr_empty_void", "qr_public_void"} {
					q := "SELECT " + name + "() AS v"
					s := fixture.Select(q, []string{"v"}, []string{"VOID"}, [][]any{{nil}})
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
					stmt.Close()
				}

				q := "SELECT qr_empty() AS v"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatalf("empty-body held preparation: %+v", fixture.ExtractDiagnostic(t, err))
				}
				defer held.Close()
				for _, route := range []string{"direct", "fresh", "held", "held again"} {
					quotedReplayEmptyFailure(t, p, b, c, held, q, route)
				}
				routineExec(t, p, b, c, "CREATE TABLE qr_empty_reached(v integer DEFAULT qr_empty())")
				identitySQL := "SELECT 'qr_empty_reached'::regclass::oid::bigint AS table_oid,'qr_empty()'::regprocedure::oid::bigint AS routine_oid"
				var tableID, calleeID int64
				if err := c.QueryRowContext(p.Ctx, identitySQL).Scan(&tableID, &calleeID); err != nil {
					t.Fatal(err)
				}
				if tableID <= 0 || calleeID <= 0 {
					t.Fatal("invalid default table/callee identities", tableID, calleeID)
				}
				defaultState := func() {
					t.Helper()
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(identitySQL,
						[]string{"table_oid", "routine_oid"}, []string{"INT8", "INT8"}, [][]any{{tableID, calleeID}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
						"SELECT a.attname,a.attnum,ad.adrelid::bigint FROM pg_attrdef ad JOIN pg_attribute a ON a.attrelid=ad.adrelid AND a.attnum=ad.adnum WHERE ad.adrelid='qr_empty_reached'::regclass ORDER BY a.attnum",
						[]string{"attname", "attnum", "adrelid"}, []string{"NAME", "INT2", "INT8"}, [][]any{{"v", int64(1), tableID}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
						"SELECT d.objsubid,d.refclassid::bigint,d.refobjid::bigint,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_attrdef ad ON ad.oid=d.objid WHERE d.classid='pg_attrdef'::regclass AND ad.adrelid='qr_empty_reached'::regclass AND ad.adnum=1 AND d.refclassid IN ('pg_proc'::regclass,'pg_class'::regclass) ORDER BY d.refclassid,d.refobjid,d.refobjsubid,d.deptype",
						[]string{"objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT4", "INT8", "INT8", "INT4", "CHAR"},
						[][]any{{int64(0), int64(1255), calleeID, int64(0), "n"}, {int64(0), int64(1259), tableID, int64(1), "a"}}))
				}
				defaultState()
				quotedReplayEmptyFailure(t, p, b, c, nil, "INSERT INTO qr_empty_reached DEFAULT VALUES", "default INSERT")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM qr_empty_reached", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))

				// Retain the actual failed handle across repair; do not replace it
				// with a fresh statement and call that failed-analysis recovery.
				routineExec(t, p, b, c, "CREATE OR REPLACE FUNCTION qr_empty() RETURNS integer LANGUAGE SQL AS $$SELECT 7$$")
				routineValue(t, p, b, c, q, "v", "INT4", int64(7))
				routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
				// Re-establish this default explicitly; retained dependent-carrier
				// refresh after routine replacement is a separate unresolved bug.
				routineExec(t, p, b, c, "ALTER TABLE qr_empty_reached ALTER COLUMN v SET DEFAULT qr_empty()")
				defaultState()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qr_empty_reached DEFAULT VALUES", 1))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM qr_empty_reached", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				if b.PG {
					return
				}

				routineExec(t, p, b, c, "CREATE TABLE qr_empty_sentinel(v integer)", "SET application_name='empty-body-target'")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qr_empty_sentinel VALUES(77)", 1))
				kept, err := c.PrepareContext(p.Ctx, "SELECT v FROM qr_empty_sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer kept.Close()
				old, err := c.QueryContext(p.Ctx, "SELECT v FROM qr_empty_sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				var before bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
				attempt := "SET check_function_bodies = false; CREATE TABLE must_not_publish(v integer); " + declarations + " CREATE TABLE qr_empty_destination(v integer DEFAULT qr_empty()); INSERT INTO qr_empty_destination DEFAULT VALUES;"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(attempt))
				want := quotedReplayEmptyDiagnostic(false)
				fixture.AssertDiagnostic(t, err, &want)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("empty-body Restore failure changed target catalog/data")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"empty-body-target"}}))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: kept, SQL: "SELECT v FROM qr_empty_sentinel"}, b.Recorder, fixture.Select("SELECT v FROM qr_empty_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname IN ('must_not_publish','qr_empty_destination')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				routineValue(t, p, b, c, q, "v", "INT4", int64(7))
				routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
				fixture.CatalogControlNativeValues(t, old, 77)
			})
		})
	}
}
