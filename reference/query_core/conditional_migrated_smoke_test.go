package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeConditionalSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, db := p.Ctx, backend.DB
			run := func(x fixture.Executor, step fixture.Step) {
				t.Helper()
				if step.SelectRows && step.Diagnostic == nil {
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						metadata, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						fixture.ScalarAssertMetadata(t, backend.PG, metadata)
					}
				}
				fixture.RunStep(t, ctx, x, backend.Recorder, step)
			}
			exec := func(q string, count int64) { run(db, fixture.Exec(q, count)) }
			query := func(q string, names, types []string, want [][]any, args ...any) {
				step := fixture.Select(q, names, types, want, args...)
				run(db, step)
				run(db, fixture.Prepared(step))
			}
			failure := func(q string, diagnostic fixture.Diagnostic, args ...any) fixture.Step {
				step := fixture.ImplicitError(q, diagnostic, args...)
				step.SelectRows, step.ErrorStage = true, "execute"
				return step
			}
			exec("CREATE TABLE t(id INTEGER,v INTEGER,b BOOLEAN,s TEXT)", 0)
			exec("INSERT INTO t VALUES(1,0,TRUE,'x'),(2,2,FALSE,NULL),(3,NULL,NULL,'y')", 3)
			cases := []struct {
				q            string
				names, types []string
				want         [][]any
				args         []any
			}{
				{"SELECT id,CASE WHEN b THEN 10 WHEN v=2 THEN 20 ELSE 30 END,CASE v WHEN 0 THEN 'zero' WHEN 2 THEN 'two' ELSE 'other' END,CASE WHEN b THEN 1 END FROM t ORDER BY id", []string{"id", "case", "case", "case"}, []string{"INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), int64(10), "zero", int64(1)}, {int64(2), int64(20), "two", nil}, {int64(3), int64(30), "other", nil}}, nil},
				{"SELECT COALESCE(s,'fallback'),COALESCE(NULL,NULL),GREATEST(v,NULL,2),LEAST(v,NULL,2),NULLIF(v,0) FROM t ORDER BY id", []string{"coalesce", "coalesce", "greatest", "least", "nullif"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT4"}, [][]any{{"x", nil, int64(2), int64(0), nil}, {"fallback", nil, int64(2), int64(2), int64(2)}, {"y", nil, int64(2), int64(2), nil}}, nil},
				{"SELECT NULLIF(1,2147483648),NULLIF(1,2.2),CASE WHEN b THEN 1 ELSE 2147483648 END FROM t WHERE id=1", []string{"nullif", "nullif", "case"}, []string{"INT4", "NUMERIC", "INT8"}, [][]any{{int64(1), "1", int64(1)}}, nil},
				{"SELECT CASE WHEN v=0 THEN 0 ELSE 10/v END,COALESCE(v,10/v) FROM t ORDER BY id", []string{"case", "coalesce"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}, {int64(5), int64(2)}, {nil, nil}}, nil},
				{"SELECT CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END,COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=1", []string{"case", "coalesce"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}}, []any{"bad"}},
				{"SELECT CASE WHEN TRUE THEN 1 ELSE 1/0 END,CASE WHEN FALSE THEN 1/0 ELSE 1 END,COALESCE(1,1/0) FROM t WHERE id=1", []string{"case", "case", "coalesce"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}}, nil},
			}
			for _, tc := range cases {
				step := fixture.Select(tc.q, tc.names, tc.types, tc.want, tc.args...)
				// PG custom planning folds the parameter cast even in skipped branches.
				if backend.PG && len(tc.args) != 0 {
					step = failure(tc.q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, tc.args...)
				}
				run(db, step)
				run(db, fixture.Prepared(step))
			}
			exec("CREATE TABLE mixed(b INTEGER,e REAL)", 0)
			exec("INSERT INTO mixed VALUES(1,2)", 1)
			query("SELECT NULLIF(b,e),NULLIF(e,b) FROM mixed", []string{"nullif", "nullif"}, []string{"FLOAT8", "FLOAT4"}, [][]any{{float64(1), float64(2)}})
			for _, tc := range []struct {
				q, name           string
				selected, skipped int
				skippedValue      int64
			}{
				{"SELECT CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END FROM t WHERE id=$2", "case", 2, 1, 1},
				{"SELECT COALESCE(v,CAST(CAST($1 AS TEXT) AS INTEGER)) FROM t WHERE id=$2", "coalesce", 3, 1, 0},
			} {
				bad := failure(tc.q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", tc.selected)
				run(db, bad)
				good := fixture.Select(tc.q, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}}, "7", tc.selected)
				run(db, good)
				stmt, err := db.PrepareContext(ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: tc.q}
				run(held, bad)
				run(held, good)
				skipped := fixture.Select(tc.q, []string{tc.name}, []string{"INT4"}, [][]any{{tc.skippedValue}}, "bad", tc.skipped)
				if backend.PG {
					skipped = failure(tc.q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}, "bad", tc.skipped)
				}
				run(held, skipped)
				stmt.Close()
			}
			for _, tc := range []struct {
				q    string
				want stuntgres.Error
			}{
				{"SELECT CASE WHEN 1 THEN 2 END FROM t", stuntgres.Error{Code: "42804", Message: "argument of CASE/WHEN must be type boolean, not type integer", Position: "18"}},
				{"SELECT COALESCE(1,TRUE) FROM t", stuntgres.Error{Code: "42804", Message: "COALESCE types integer and boolean cannot be matched", Position: "19"}},
				{"SELECT CASE WHEN TRUE THEN 1 ELSE FALSE END FROM t", stuntgres.Error{Code: "42804", Message: "CASE types boolean and integer cannot be matched", Position: "28"}},
				{"SELECT NULLIF(1,TRUE) FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "8", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
				{"SELECT COALESCE(1,'bad') FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "19"}},
			} {
				step := failure(tc.q, fixture.MigrationDiagnostic(tc.want))
				run(db, step)
				step.ErrorStage = "prepare"
				run(db, fixture.Prepared(step))
			}
			planSQL := "SELECT CASE WHEN v=0 THEN 0 ELSE 1/0 END FROM t"
			plan, err := db.PrepareContext(ctx, planSQL)
			if err != nil {
				t.Fatal("Prepare folded execution-planning expression", err)
			}
			defer plan.Close()
			run(fixture.HeldStatement{Executor: db, Statement: plan, SQL: planSQL}, failure(planSQL, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			plan.Close()
			exec("CREATE TABLE d(a INTEGER DEFAULT CASE WHEN TRUE THEN COALESCE(NULL,7) ELSE 1/0 END,b INTEGER DEFAULT NULLIF(2,3),c INTEGER DEFAULT GREATEST(NULL,4),e INTEGER DEFAULT LEAST(NULL,5))", 0)
			exec("INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			exec("CREATE TABLE bd(a BOOL DEFAULT CASE WHEN TRUE THEN FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,b BOOL DEFAULT CASE WHEN TRUE THEN TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,c BOOL DEFAULT COALESCE(FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE),e BOOL DEFAULT COALESCE(TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE))", 0)
			exec("INSERT INTO bd VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			boolRow := []any{false, true, false, true}
			query("SELECT a,b,c,e FROM bd", []string{"a", "b", "c", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{boolRow})
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var redump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &redump); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatal("conditional default dump is not stable")
				}
			}
			exec("INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			exec("INSERT INTO bd VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			query("SELECT a,b,c,e FROM bd", []string{"a", "b", "c", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{boolRow, boolRow})
			row := []any{int64(7), int64(2), int64(4), int64(5)}
			query("SELECT a,b,c,e FROM d", []string{"a", "b", "c", "e"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{row, row})
			atomic := fixture.ImplicitError("UPDATE t SET v=CASE WHEN id=1 THEN 9 ELSE 10/(id-2) END", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			atomic.ErrorStage = "execute"
			run(db, atomic)
			query("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(2)}, {nil}})
		})
	}
}
