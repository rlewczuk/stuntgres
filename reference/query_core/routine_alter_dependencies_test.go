package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAlterDependencies(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ra_hard")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE SCHEMA ra_hard",
			"CREATE FUNCTION ra_callee(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1000$$",
			"CREATE VIEW ra_view AS SELECT ra_callee(1) AS v",
			"CREATE FUNCTION ra_standard() RETURNS bigint RETURN (SELECT ra_callee(1))",
			"CREATE FUNCTION ra_hidden(x bigint DEFAULT ra_callee(1)) RETURNS bigint RETURN x",
			"CREATE FUNCTION ra_soft() RETURNS bigint LANGUAGE SQL AS $$SELECT ra_callee(1)$$",
			"CREATE TABLE ra_data(v bigint DEFAULT ra_callee(1), CHECK(ra_callee(v)>1000))",
			"CREATE TABLE ra_rule_log(v bigint)",
			"CREATE VIEW ra_rule_input AS SELECT v FROM ra_rule_log",
			"CREATE RULE ra_insert AS ON INSERT TO ra_rule_input DO INSTEAD INSERT INTO ra_rule_log VALUES(ra_callee(NEW.v))",
			"CREATE TABLE ra_rule_condition_input(v bigint)",
			"CREATE TABLE ra_rule_condition_log(v bigint)",
			"CREATE RULE ra_condition AS ON INSERT TO ra_rule_condition_input WHERE ra_callee(NEW.v)=1001 DO ALSO INSERT INTO ra_rule_condition_log VALUES(NEW.v)")
		oid := routineAlterOID(t, p, c, "ra_callee")
		routineExec(t, p, b, c, "ALTER FUNCTION ra_callee(bigint) RENAME TO ra_hard_callee", "ALTER FUNCTION ra_hard_callee(bigint) SET SCHEMA ra_hard",
			"CREATE FUNCTION ra_callee(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+2000$$")
		routineAlterSameOID(t, p, c, "ra_hard_callee", oid)
		for _, q := range []string{"SELECT v FROM ra_view", "SELECT ra_standard() AS v", "SELECT ra_hidden() AS v", "SELECT ra_hard.ra_hard_callee(1) AS v"} {
			if q == "SELECT v FROM ra_view" {
				routineValueWithNativeNullability(t, p, b, c, q, "v", "INT8", int64(1001), true, true)
			} else {
				routineValue(t, p, b, c, q, "v", "INT8", int64(1001))
			}
		}
		routineValue(t, p, b, c, "SELECT ra_callee(1) AS v", "v", "INT8", int64(2001))
		routineValue(t, p, b, c, "SELECT ra_soft() AS v", "v", "INT8", int64(2001))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_data DEFAULT VALUES", 1))
		checkError := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "ra_data" violates check constraint "ra_data_v_check"`)
		checkError.Detail = "Failing row contains (-1)."
		checkError.Schema, checkError.Table, checkError.Constraint = "public", "ra_data", "ra_data_v_check"
		routineDropError(t, p, b, c, "INSERT INTO ra_data VALUES(-1)", checkError)
		routineExec(t, p, b, c, "CREATE FUNCTION ra_operator(integer,integer) RETURNS boolean RETURN $1=$2", "CREATE OPERATOR public.#=# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=ra_operator)", "ALTER FUNCTION ra_operator(integer,integer) RENAME TO ra_operator_new", "ALTER FUNCTION ra_operator_new(integer,integer) SET SCHEMA ra_hard")
		routineAlterRows(t, p, b, c, "SELECT 4 OPERATOR(public.#=#) 4 AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_rule_input VALUES(1)", 1))
		routineAlterRows(t, p, b, c, "SELECT v FROM ra_data", []string{"v"}, []string{"INT8"}, [][]any{{int64(1001)}})
		routineAlterRows(t, p, b, c, "SELECT v FROM ra_rule_log", []string{"v"}, []string{"INT8"}, [][]any{{int64(1001)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_rule_input SELECT ra_hard.ra_hard_callee(1)", 1))
		routineAlterRows(t, p, b, c, "SELECT v FROM ra_rule_log ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1001)}, {int64(2001)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_rule_condition_input VALUES(1),(-1)", 2))
		routineAlterRows(t, p, b, c, "SELECT v FROM ra_rule_condition_log", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}})
		routineAlterRows(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='ra_hard.ra_hard_callee(bigint)'::regprocedure AND refclassid='pg_namespace'::regclass AND refobjid='ra_hard'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
	})
}

func TestReferenceRoutineAlterRuleBuiltinCaptures(t *testing.T) {
	for _, tc := range routineBuiltinCaptureCases() {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_rule_capture_owner", "ra_rule_capture_reader"})
			fixture.SchemaReferenceCleanup(t, "ra_rule_capture")
			routineBuiltinCaptureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				restore := routineAlterBuiltinCleanup(t, p, c, tc.signature)
				defer restore()
				routineExec(t, p, b, c,
					"CREATE ROLE ra_rule_capture_owner", "CREATE ROLE ra_rule_capture_reader",
					"CREATE SCHEMA ra_rule_capture AUTHORIZATION ra_rule_capture_owner",
					"CREATE TABLE ra_rule_capture_input(v integer)",
					"CREATE TABLE ra_rule_capture_log(v "+tc.typ+")",
					"CREATE RULE ra_rule_capture_insert AS ON INSERT TO ra_rule_capture_input DO INSTEAD INSERT INTO ra_rule_capture_log "+tc.source,
					"GRANT INSERT ON ra_rule_capture_input TO ra_rule_capture_reader",
					"GRANT USAGE ON SCHEMA public,ra_rule_capture TO ra_rule_capture_reader")
				args := tc.signature[strings.IndexByte(tc.signature, '('):]
				current := "ra_rule_capture.ra_rule_callable" + args
				routineExec(t, p, b, c,
					"ALTER FUNCTION "+tc.signature+" RENAME TO ra_rule_callable",
					"ALTER FUNCTION pg_catalog.ra_rule_callable"+args+" SET SCHEMA ra_rule_capture",
					"ALTER FUNCTION "+current+" OWNER TO ra_rule_capture_owner")
				for _, prepared := range []bool{false, true} {
					insert := fixture.Exec("INSERT INTO ra_rule_capture_input VALUES(1)", int64(len(tc.rows)))
					if prepared {
						insert = fixture.Prepared(insert)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, insert)
					routineAlterRows(t, p, b, c, "SELECT v FROM ra_rule_capture_log ORDER BY v", []string{"v"}, []string{tc.typ}, tc.rows)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ra_rule_capture_log", int64(len(tc.rows))))
				}
				routineExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+current+" FROM PUBLIC", "SET ROLE ra_rule_capture_reader")
				noun := "aggregate"
				if tc.name == "window" {
					noun = "function"
				}
				denied := fixture.ErrorDiagnostic("INSERT INTO ra_rule_capture_input VALUES(1)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for "+noun+" ra_rule_callable"))
				denied.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
				routineExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION "+current+" TO PUBLIC", "SET ROLE ra_rule_capture_reader")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_rule_capture_input VALUES(1)", int64(len(tc.rows))))
				routineExec(t, p, b, c, "RESET ROLE")
				routineAlterRows(t, p, b, c, "SELECT v FROM ra_rule_capture_log ORDER BY v", []string{"v"}, []string{tc.typ}, tc.rows)
			})
		})
	}
}

func TestReferenceRoutineAlterPrepared(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ra_prepared")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE SCHEMA ra_prepared", "CREATE FUNCTION ra_held(integer) RETURNS integer RETURN $1+1", "CREATE VIEW ra_held_view AS SELECT ra_held(4) AS v", "PREPARE ra_sql(integer) AS SELECT ra_held($1) AS v")
		q := "SELECT ra_held($1::integer) AS v"
		old, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		view, err := c.PrepareContext(p.Ctx, "SELECT v FROM ra_held_view")
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: old, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, 4))
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT ra_held(4) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		routineExec(t, p, b, c, "ALTER FUNCTION ra_held(integer) RENAME TO ra_held_new", "ALTER FUNCTION ra_held_new(integer) SET SCHEMA ra_prepared")
		d := fixture.ControlsReferenceDiagnostic("42883", "function ra_held(integer) does not exist")
		d.Position, d.Hint = "8", "No function matches the given name and argument types. You might need to add explicit type casts."
		s := fixture.ErrorDiagnostic(q, d, 4)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: old, SQL: q}, b.Recorder, s)
		d.Position = "35"
		s = fixture.ErrorDiagnostic("EXECUTE ra_sql(4)", d)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		viewStep := fixture.Select("SELECT v FROM ra_held_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
		viewStep.CheckMetadata = routineScalarMetadata(b, "v", "INT4", true, true)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: view, SQL: viewStep.Query}, b.Recorder, viewStep)
		var v int64
		if !retained.Next() {
			t.Fatal("retained row", retained.Err())
		}
		if err := retained.Scan(&v); err != nil || v != 5 {
			t.Fatal(v, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained rows", retained.Err())
		}
		retained.Close()
		routineExec(t, p, b, c, "CREATE FUNCTION ra_held(integer) RETURNS integer RETURN $1+100")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: old, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(104)}}, 4))
		routineAlterRows(t, p, b, c, "EXECUTE ra_sql(4)", []string{"v"}, []string{"INT4"}, [][]any{{int64(104)}})
		newQ := "SELECT ra_prepared.ra_held_new($1::integer) AS answer"
		fresh, err := c.PrepareContext(p.Ctx, newQ)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		for _, input := range []int64{5, 9} {
			s := fixture.Select(newQ, []string{"answer"}, []string{"INT4"}, [][]any{{input + 1}}, input)
			s.CheckMetadata = routineScalarMetadata(b, "answer", "INT4", false, false)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: newQ}, b.Recorder, s)
		}
		routineExec(t, p, b, c, "DEALLOCATE ra_sql")
	})
}

func TestReferenceRoutineAlterFormalQualifier(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE FUNCTION ra_qualified(x integer) RETURNS integer LANGUAGE SQL AS $$SELECT ra_qualified.x+1$$", "CREATE FUNCTION ra_frozen(x integer) RETURNS integer RETURN ra_frozen.x+1")
		routineValue(t, p, b, c, "SELECT ra_qualified(4) AS v", "v", "INT4", int64(5))
		routineExec(t, p, b, c, "ALTER FUNCTION ra_qualified(integer) RENAME TO ra_qualified_new", "ALTER FUNCTION ra_frozen(integer) RENAME TO ra_frozen_new")
		routineValue(t, p, b, c, "SELECT ra_frozen_new(4) AS v", "v", "INT4", int64(5))
		d := fixture.ControlsReferenceDiagnostic("42P01", `missing FROM-clause entry for table "ra_qualified"`)
		d.InternalPosition, d.InternalQuery = "8", "SELECT ra_qualified.x+1"
		d.Context = "SQL function \"ra_qualified_new\" during inlining"
		// Preserve the established native body-coordinate boundary without
		// pretending that native has PostgreSQL's SQL-function inliner.
		native := fixture.ControlsReferenceDiagnostic("42P01", d.Message)
		native.Position, native.Context = "8", "SQL function \"ra_qualified_new\" statement 1"
		s := fixture.ErrorDiagnostic("SELECT ra_qualified_new(4) AS v", native)
		s.PostgresDiagnostic = &d
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		routineExec(t, p, b, c, "DROP FUNCTION ra_qualified_new(integer)", "CREATE FUNCTION ra_qualified_new(x integer) RETURNS integer LANGUAGE SQL AS $$SELECT ra_qualified_new.x+1$$")
		routineValue(t, p, b, c, "SELECT ra_qualified_new(4) AS v", "v", "INT4", int64(5))
	})
}
