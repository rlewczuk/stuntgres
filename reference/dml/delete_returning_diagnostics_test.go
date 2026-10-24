package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, r := backend.DB, backend.Recorder
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1),(2)", 2)
			for _, tc := range []struct {
				name, query string
				want        fixture.Diagnostic
			}{
				{"incomplete", "DELETE FROM t RETURNING", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "24"}},
				{"column", "DELETE FROM t RETURNING zzzzz", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "25"}},
				{"column_utf8", "/*é*/ DELETE FROM t RETURNING zzzzz", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "31"}},
				{"old_column", "DELETE FROM t RETURNING old.zzzzz", fixture.Diagnostic{Code: "42703", Message: "column old.zzzzz does not exist", Position: "25"}},
				{"qualifier", "DELETE FROM t RETURNING x.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "x"`, Position: "25"}},
				{"hidden_target", "DELETE FROM t AS d RETURNING t.id", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "30", Hint: `Perhaps you meant to reference the table alias "d".`}},
				{"hidden_old", "DELETE FROM t RETURNING WITH (OLD AS o) old.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "41"}},
				{"hidden_new", "DELETE FROM t RETURNING WITH (NEW AS n) new.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "new"`, Position: "41"}},
				{"cross_hidden_old", "DELETE FROM t RETURNING WITH (OLD AS new) old.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "43"}},
				{"cross_hidden_new", "DELETE FROM t RETURNING WITH (NEW AS old) new.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "new"`, Position: "43"}},
				{"cross_collision", "DELETE FROM t RETURNING WITH (OLD AS new, NEW AS new) id", fixture.Diagnostic{Code: "42712", Message: `table name "new" specified more than once`, Position: "43"}},
				{"duplicate_old", "DELETE FROM t RETURNING WITH (OLD AS o, OLD AS p) id", fixture.Diagnostic{Code: "42601", Message: "OLD cannot be specified multiple times", Position: "41"}},
				{"duplicate_new", "DELETE FROM t RETURNING WITH (NEW AS n, NEW AS p) id", fixture.Diagnostic{Code: "42601", Message: "NEW cannot be specified multiple times", Position: "41"}},
				{"target_collision", "DELETE FROM t RETURNING WITH (OLD AS t) id", fixture.Diagnostic{Code: "42712", Message: `table name "t" specified more than once`, Position: "31"}},
				{"alias_collision", "DELETE FROM t RETURNING WITH (OLD AS o, NEW AS o) id", fixture.Diagnostic{Code: "42712", Message: `table name "o" specified more than once`, Position: "41"}},
				{"old_where", "DELETE FROM t WHERE old.id=1 RETURNING id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "21"}},
				{"aggregate", "DELETE FROM t RETURNING count(*)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "25"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						t.Run(stage, func(t *testing.T) {
							s := fixture.ImplicitError(tc.query, tc.want)
							s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, stage
							fixture.RunStep(t, p.Ctx, db, r, s)
							fixture.DeleteReferenceRows(t, p, db, r, 1, 2)
						})
					}
				})
			}
			// Star expansion is checked before scanning even an empty target.
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE z()", 0)
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DELETE FROM z RETURNING *", fixture.Diagnostic{Code: "42601", Message: "RETURNING must have at least one column", Position: "25"})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, db, r, s)
				fixture.DeleteReferenceRows(t, p, db, r, 1, 2)
			}
		})
	}
}

func TestReferenceDeleteReturningParameterOccurrenceDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, r := backend.DB, backend.Recorder
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT)", 0)
			for _, populated := range []bool{false, true} {
				wantRows := [][]any{}
				if populated {
					fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1),(2)", 2)
					wantRows = [][]any{{int64(1)}, {int64(2)}}
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("DELETE FROM t RETURNING $1,$1::int4", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "25"}, 2)
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, db, r, s)
					fixture.RunStep(t, p.Ctx, db, r, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
				}
			}
		})
	}
}

func TestReferenceDeleteReturningParameterOrderControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			a, _ := fixture.LockConnections(t, p.Ctx, db)
			r := backend.Recorder
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
			for _, query := range []string{
				"DELETE FROM t RETURNING $1::int4 AS typed,$1 AS repeated",
				"DELETE FROM t WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated",
			} {
				for _, prepared := range []bool{false, true} {
					var executor fixture.Executor = a
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = a.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						executor = fixture.HeldStatement{Executor: a, Statement: held, SQL: query}
					}
					for step, want := range [][][]any{{{int64(2), int64(2)}, {int64(2), int64(2)}}, {}, {{int64(2), int64(2)}, {int64(2), int64(2)}}} {
						if step != 1 {
							fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2),(2)", 2)
						}
						s := fixture.Select(query, []string{"typed", "repeated"}, []string{"INT4", "INT4"}, want, 2)
						s.Bag = true
						fixture.RunStep(t, p.Ctx, executor, r, s)
					}
					if held != nil {
						held.Close()
					}
					fixture.RunStep(t, p.Ctx, a, r, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
				}
			}
		})
	}
}

func TestReferenceDeleteReturningRegclassBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, r := backend.DB, backend.Recorder
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE rt(v TEXT)", 0)
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					query          string
					columns, types []string
					values         []any
				}{
					{"DELETE FROM rt RETURNING old.v::regclass,new.v::regclass", []string{"v", "v"}, []string{"REGCLASS", "REGCLASS"}, []any{nil, nil}},
					{"DELETE FROM rt RETURNING new.v::regclass", []string{"v"}, []string{"REGCLASS"}, []any{nil}},
					{"DELETE FROM rt RETURNING v::regclass", []string{"v"}, []string{"REGCLASS"}, []any{nil}},
					{"DELETE FROM rt RETURNING CAST(v AS regclass)", []string{"v"}, []string{"REGCLASS"}, []any{nil}},
				} {
					fixture.LockExec(t, p.Ctx, db, "INSERT INTO rt VALUES(NULL)", 1)
					s := fixture.Select(tc.query, tc.columns, tc.types, [][]any{tc.values})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, db, r, s)
				}
			}
		})
	}
}
