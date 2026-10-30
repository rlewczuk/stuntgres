package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// sqlBodiesScenario exercises the same contract on every available backend.
func sqlBodiesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func TestReferenceRoutineSQLBodiesStatements(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE body_balance(id integer PRIMARY KEY, balance integer)",
			"CREATE FUNCTION body_bump(delta integer) RETURNS integer LANGUAGE SQL AS $$UPDATE body_balance SET balance=balance+delta WHERE id=1; SELECT balance FROM body_balance WHERE id=1$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_balance VALUES (1,10)", 1))
		q := "SELECT body_bump($1) AS answer"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		routineValueWithNativeNullability(t, p, b, c, "SELECT balance AS v FROM body_balance", "v", "INT4", int64(10), true, true)
		for _, want := range []int64{15, 20} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
				fixture.Select(q, []string{"answer"}, []string{"INT4"}, [][]any{{want}}, 5))
		}
		routineValueWithNativeNullability(t, p, b, c, "SELECT balance AS v FROM body_balance", "v", "INT4", int64(20), true, true)
		routineExec(t, p, b, c,
			"CREATE TABLE body_terminal(v integer)",
			"CREATE FUNCTION body_insert() RETURNS integer LANGUAGE SQL AS $$INSERT INTO body_terminal VALUES (7),(7),(7) RETURNING v$$",
			"CREATE FUNCTION body_update() RETURNS integer LANGUAGE SQL AS $$UPDATE body_terminal SET v=8 RETURNING v$$",
			"CREATE FUNCTION body_delete() RETURNS integer LANGUAGE SQL AS $$DELETE FROM body_terminal RETURNING v$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT body_insert() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_terminal", "v", "INT8", int64(3), false, true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT body_update() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT body_delete() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_terminal", "v", "INT8", int64(0), false, true)
		routineExec(t, p, b, c,
			"CREATE FUNCTION body_merge() RETURNS integer LANGUAGE SQL AS $$MERGE INTO body_terminal t USING (VALUES (9)) s(v) ON t.v=s.v WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.v) RETURNING t.v$$")
		routineValue(t, p, b, c, "SELECT body_merge() AS v", "v", "INT4", int64(9))
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_terminal", "v", "INT8", int64(1), false, true)
	})
}

func TestReferenceRoutineSQLBodiesArguments(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE body_names(x integer)",
			"CREATE FUNCTION body_formal(x integer) RETURNS integer LANGUAGE SQL AS $$SELECT x+body_formal.x FROM body_names$$",
			"CREATE FUNCTION body_alias(x integer) RETURNS integer LANGUAGE SQL AS $$SELECT x+body_alias.x FROM body_names AS body_alias$$",
			"CREATE FUNCTION body_slots(IN x integer) RETURNS integer LANGUAGE SQL AS $$SELECT x+body_slots.x+$1$$",
			"CREATE FUNCTION body_cte(x integer) RETURNS integer LANGUAGE SQL AS $$WITH q(x) AS (SELECT 11) SELECT x+body_cte.x FROM q$$",
			"CREATE FUNCTION body_quotes() RETURNS integer LANGUAGE SQL AS $outer$SELECT ';'::text; /* ; */ SELECT length($inner$a;b$inner$)$outer$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_names VALUES(10)", 1))
		for _, tc := range []struct {
			q string
			v int64
		}{
			{"SELECT body_formal(5) AS v", 15}, {"SELECT body_alias(5) AS v", 20},
			{"SELECT body_slots(5) AS v", 15}, {"SELECT body_cte(5) AS v", 16}, {"SELECT body_quotes() AS v", 3},
		} {
			routineValue(t, p, b, c, tc.q, "v", "INT4", tc.v)
		}
	})
}

func TestReferenceRoutineSQLBodiesStandard(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION body_return(x integer) RETURNS integer RETURN x+1",
			"CREATE TABLE body_atomic(v integer)",
			"CREATE FUNCTION body_atomic_bump(x integer) RETURNS integer LANGUAGE SQL BEGIN ATOMIC UPDATE body_atomic SET v=v+x; SELECT v FROM body_atomic; END",
			"CREATE FUNCTION body_nonterminal() RETURNS integer BEGIN ATOMIC RETURN 1; RETURN 2; END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_atomic VALUES(10)", 1))
		routineValue(t, p, b, c, "SELECT body_return(4) AS v", "v", "INT4", int64(5))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT body_atomic_bump(5) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(15)}}))
		routineValue(t, p, b, c, "SELECT body_nonterminal() AS v", "v", "INT4", int64(2))
	})
}

func TestReferenceRoutineSQLBodiesVoid(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE body_void_log(v integer)", "CREATE SEQUENCE body_void_ticks",
			"CREATE FUNCTION body_void() RETURNS void LANGUAGE SQL AS $$INSERT INTO body_void_log VALUES(1); SELECT nextval('body_void_ticks') FROM generate_series(1,3)$$",
			"CREATE FUNCTION body_void_empty() RETURNS void LANGUAGE SQL AS $$$$",
			"CREATE FUNCTION body_void_atomic_empty() RETURNS void BEGIN ATOMIC END")
		s := fixture.Select("SELECT body_void() AS v", []string{"v"}, []string{"VOID"}, [][]any{{nil}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			cols, err := rows.ColumnTypes()
			if err != nil || len(cols) != 1 {
				t.Fatal(cols, err)
			}
			col := cols[0]
			nullable, nullableOK := col.Nullable()
			length, lengthOK := col.Length()
			precision, scale, decimalOK := col.DecimalSize()
			wantScan := reflect.TypeFor[any]()
			if !b.PG {
				wantScan = reflect.TypeFor[string]()
			}
			if col.ScanType() != wantScan || nullable || nullableOK || lengthOK || decimalOK {
				t.Errorf("VOID metadata: scan=%v nullable=%v/%v length=%v/%v decimal=%v,%v/%v", col.ScanType(), nullable, nullableOK, length, lengthOK, precision, scale, decimalOK)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		routineValue(t, p, b, c, "SELECT currval('body_void_ticks') AS v", "v", "INT8", int64(3))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT body_void_empty() IS NULL AS v, body_void_atomic_empty() IS NULL AS w", []string{"v", "w"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}

// Initial full diagnostic predictions are independent of native behavior. The
// oracle-first run resolves positions/context and stage before the native red.
func sqlBodiesDiagnostic(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, query bool, want fixture.Diagnostic, prepareStage bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			stage := "execute"
			var err error
			var stmt *sql.Stmt
			if prepared {
				stmt, err = c.PrepareContext(p.Ctx, q)
				if err != nil {
					stage = "prepare"
				} else {
					defer stmt.Close()
				}
			}
			if err == nil {
				if query {
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = c.QueryContext(p.Ctx, q)
					}
					if rows != nil {
						for rows.Next() {
						}
						if err == nil {
							err = rows.Err()
						}
						rows.Close()
					}
				} else if prepared {
					_, err = stmt.ExecContext(p.Ctx)
				} else {
					_, err = c.ExecContext(p.Ctx, q)
				}
			}
			wantStage := "execute"
			if prepared && prepareStage {
				wantStage = "prepare"
			}
			if stage != wantStage {
				t.Errorf("diagnostic stage=%s want=%s query=%s", stage, wantStage, q)
			}
			fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, ErrorStage: wantStage}, stage)
		})
	}
}

func TestReferenceRoutineSQLBodiesValidation(t *testing.T) {
	for _, tc := range []struct{ name, q, code, message, detail, context string }{
		{"final", "CREATE FUNCTION body_bad_final() RETURNS integer LANGUAGE SQL AS $$INSERT INTO body_invalid_log VALUES(1)$$", "42P13", "return type mismatch in function declared to return integer", "Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING.", "SQL function \"body_bad_final\""},
		{"empty", "CREATE FUNCTION body_bad_empty() RETURNS integer LANGUAGE SQL AS $$$$", "42P13", "return type mismatch in function declared to return integer", "Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING.", "SQL function \"body_bad_empty\""},
		{"duplicate", "CREATE FUNCTION body_duplicate(x integer,x integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", "42P13", "parameter name \"x\" used more than once", "", ""},
		{"ambiguous", "CREATE FUNCTION body_ambiguous(x integer) RETURNS integer LANGUAGE SQL AS $$SELECT x FROM body_left,body_right$$", "42702", "column reference \"x\" is ambiguous", "", "SQL function \"body_ambiguous\""},
		{"standard_missing", "CREATE FUNCTION body_standard_missing() RETURNS integer BEGIN ATOMIC SELECT x FROM body_absent; END", "42P01", "relation \"body_absent\" does not exist", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "CREATE TABLE body_invalid_log(v integer)", "CREATE TABLE body_left(x integer)", "CREATE TABLE body_right(x integer)")
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				switch tc.name {
				case "duplicate":
					d.Position = "42"
				case "ambiguous", "standard_missing":
					d.Position, d.Context = "84", ""
				}
				sqlBodiesDiagnostic(t, p, c, tc.q, false, d, false)
				routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_invalid_log", "v", "INT8", int64(0), false, true)
			})
		})
	}
	t.Run("sequential reanalysis", func(t *testing.T) {
		sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c,
				"CREATE SEQUENCE body_recheck_ticks", "CREATE TABLE body_recheck_source(x integer)",
				"CREATE FUNCTION body_recheck() RETURNS integer LANGUAGE SQL AS $$SELECT nextval('body_recheck_ticks'); SELECT x FROM body_recheck_source$$",
				"DROP TABLE body_recheck_source", "CREATE TABLE body_recheck_source(x text)")
			d := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
			d.Detail = "Actual return type is text."
			d.Context = "SQL function \"body_recheck\" statement 2"
			sqlBodiesDiagnostic(t, p, c, "SELECT body_recheck()", true, d, false)
			// Each failed invocation reaches its first command, despite an empty
			// terminal relation. Sequence increments are nontransactional.
			routineValue(t, p, b, c, "SELECT currval('body_recheck_ticks') AS v", "v", "INT8", int64(2))
		})
	})
}

func TestReferenceRoutineSQLBodiesRestrictions(t *testing.T) {
	for _, tag := range []string{"COMMIT", "SAVEPOINT body_save"} {
		t.Run(tag, func(t *testing.T) {
			sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				name := "body_transaction"
				label := "COMMIT"
				if tag != "COMMIT" {
					label = "SAVEPOINT"
				}
				routineExec(t, p, b, c, "CREATE FUNCTION "+name+"() RETURNS integer LANGUAGE SQL AS $$"+tag+"; SELECT 1$$")
				d := fixture.ControlsReferenceDiagnostic("0A000", label+" is not allowed in an SQL function")
				d.Context = "SQL function \"body_transaction\" statement 1"
				sqlBodiesDiagnostic(t, p, c, "SELECT body_transaction()", true, d, false)
				d = fixture.ControlsReferenceDiagnostic("0A000", label+" is not yet supported in unquoted SQL function body")
				sqlBodiesDiagnostic(t, p, c, "CREATE FUNCTION body_standard_transaction() RETURNS integer BEGIN ATOMIC "+tag+"; SELECT 1; END", false, d, false)
			})
		})
	}
}

func TestReferenceRoutineSQLBodiesDependencies(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE body_capture_source(v integer)",
			"CREATE FUNCTION body_capture() RETURNS integer BEGIN ATOMIC SELECT v FROM body_capture_source; END",
			"ALTER TABLE body_capture_source RENAME TO body_capture_renamed",
			"CREATE TABLE body_capture_source(v integer)",
			"CREATE FUNCTION body_overload(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1000$$",
			"CREATE FUNCTION body_selected() RETURNS bigint RETURN body_overload(1)",
			"CREATE FUNCTION body_overload(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+2000$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_capture_renamed VALUES(17)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_capture_source VALUES(99)", 1))
		routineValue(t, p, b, c, "SELECT body_capture() AS v", "v", "INT4", int64(17))
		routineValue(t, p, b, c, "SELECT body_selected() AS v", "v", "INT8", int64(1001))
		routineValue(t, p, b, c, "SELECT body_overload(1) AS v", "v", "INT8", int64(2001))
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table body_capture_renamed because other objects depend on it")
		d.Detail = "function body_capture() depends on table body_capture_renamed"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		sqlBodiesDiagnostic(t, p, c, "DROP TABLE body_capture_renamed", false, d, false)
		routineExec(t, p, b, c,
			"CREATE VIEW body_capture_view AS SELECT v FROM body_capture_source",
			"CREATE FUNCTION body_view_read() RETURNS integer RETURN (SELECT v FROM body_capture_view)",
			"ALTER VIEW body_capture_view RENAME TO body_view_renamed",
			"CREATE VIEW body_capture_view AS SELECT 3 AS v",
			"CREATE TABLE body_soft_source(v integer)",
			"CREATE FUNCTION body_soft() RETURNS integer LANGUAGE SQL AS $$SELECT v FROM body_soft_source$$",
			"DROP TABLE body_soft_source",
			"CREATE TABLE body_soft_source(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_soft_source VALUES(23)", 1))
		routineValue(t, p, b, c, "SELECT body_view_read() AS v", "v", "INT4", int64(99))
		routineValue(t, p, b, c, "SELECT body_soft() AS v", "v", "INT4", int64(23))
		routineExec(t, p, b, c, "DROP TABLE body_capture_renamed CASCADE")
		d = fixture.ControlsReferenceDiagnostic("42883", "function body_capture() does not exist")
		d.Position = "8"
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		sqlBodiesDiagnostic(t, p, c, "SELECT body_capture()", true, d, true)
		d = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function body_overload(bigint) because other objects depend on it")
		d.Detail = "function body_selected() depends on function body_overload(bigint)"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		sqlBodiesDiagnostic(t, p, c, "DROP FUNCTION body_overload(bigint)", false, d, false)
	})
}

func TestReferenceRoutineSQLBodiesProcedureDeclarations(t *testing.T) {
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE body_proc_log(v integer)",
			"CREATE PROCEDURE body_procedure(IN x integer) LANGUAGE SQL AS $$INSERT INTO body_proc_log VALUES(x); SELECT $1$$",
			"CREATE PROCEDURE body_standard_procedure(x integer) BEGIN ATOMIC INSERT INTO body_proc_log VALUES(x); END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT proargnames::text AS args FROM pg_proc WHERE proname='body_procedure'", []string{"args"}, []string{"TEXT"}, [][]any{{"{x}"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT proname,prokind,prorettype::regtype::text AS result FROM pg_proc WHERE proname IN ('body_procedure','body_standard_procedure') ORDER BY proname", []string{"proname", "prokind", "result"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"body_procedure", "p", "void"}, {"body_standard_procedure", "p", "void"}}))
		d := fixture.ControlsReferenceDiagnostic("42809", "body_procedure(integer) is a procedure")
		d.Hint = "To call a procedure, use CALL."
		d.Position = "8"
		sqlBodiesDiagnostic(t, p, c, "SELECT body_procedure(1)", true, d, true)
		d = fixture.ControlsReferenceDiagnostic("42723", "function \"body_procedure\" already exists with same argument types")
		sqlBodiesDiagnostic(t, p, c, "CREATE FUNCTION body_procedure(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", false, d, false)
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM body_proc_log", "v", "INT8", int64(0), false, true)
	})
}
