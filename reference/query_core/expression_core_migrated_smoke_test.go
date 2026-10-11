package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSmokeExpressionCoreSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		func() {
			ctx, db := p.Ctx, backend.DB
			run := func(step fixture.Step) { t.Helper(); fixture.RunStep(t, ctx, db, backend.Recorder, step) }
			exec := func(q string, want int64) { t.Helper(); run(fixture.Exec(q, want)) }
			exec("CREATE TABLE t(id INTEGER,v INTEGER,b BIGINT,s TEXT,f BOOLEAN)", 0)
			exec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)", 4)
			all := [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}
			cases := []struct {
				q            string
				names, types []string
				want         [][]any
				args         []any
			}{
				{"SELECT id,1,2147483648,'x',TRUE,FALSE,NULL FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "INT4", "INT8", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{int64(1), int64(1), int64(2147483648), "x", true, false, nil}, {int64(2), int64(1), int64(2147483648), "x", true, false, nil}, {int64(3), int64(1), int64(2147483648), "x", true, false, nil}, {int64(4), int64(1), int64(2147483648), "x", true, false, nil}}, nil},
				{"SELECT 7 FROM t WHERE v=1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}, nil},
				{"SELECT $1,$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"TEXT", "TEXT"}, [][]any{{"hi", "hi"}, {"hi", "hi"}, {"hi", "hi"}, {"hi", "hi"}}, []any{"hi"}},
				{"SELECT $1 FROM t WHERE s=$1 ORDER BY id", []string{"?column?"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, []any{"x"}},
				{"SELECT id FROM t WHERE TRUE ORDER BY id", []string{"id"}, []string{"INT4"}, all, nil},
				{"SELECT id FROM t WHERE f ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}, nil},
				{"SELECT id FROM t WHERE FALSE", []string{"id"}, []string{"INT4"}, [][]any{}, nil},
				{"SELECT id FROM t WHERE NULL", []string{"id"}, []string{"INT4"}, [][]any{}, nil},
				{"SELECT v FROM t WHERE v=NULL", []string{"v"}, []string{"INT4"}, [][]any{}, nil},
				{"SELECT id FROM t WHERE 'yes' ORDER BY id", []string{"id"}, []string{"INT4"}, all, nil},
				{"SELECT id FROM t WHERE 'no'", []string{"id"}, []string{"INT4"}, [][]any{}, nil},
				{"SELECT id FROM t WHERE $1 ORDER BY id", []string{"id"}, []string{"INT4"}, all, []any{true}},
				{"SELECT id FROM t WHERE $1", []string{"id"}, []string{"INT4"}, [][]any{}, []any{false}},
				{"SELECT id FROM t WHERE $1", []string{"id"}, []string{"INT4"}, [][]any{}, []any{nil}},
			}
			for _, tc := range cases {
				step := fixture.Select(tc.q, tc.names, tc.types, tc.want, tc.args...)
				run(step)
				run(fixture.Prepared(step))
			}
			held, err := db.PrepareContext(ctx, "SELECT id FROM t WHERE v=$1 ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for i, arg := range []any{1, 2, nil} {
				want := [][]any{{int64(1)}, {int64(4)}}
				if i == 1 {
					want = [][]any{{int64(3)}}
				}
				if i == 2 {
					want = [][]any{}
				}
				step := fixture.Select("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, want, arg)
				fixture.RunStep(t, ctx, fixture.HeldStatement{db, held, step.Query}, backend.Recorder, step)
			}
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"SELECT missing FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
				{"SELECT /*é*/ missing FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
				{"SELECT id FROM t WHERE missing=1", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "24"}},
				{"SELECT id FROM t WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
				{"SELECT id FROM t WHERE 'truth'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "24"}},
				{"SELECT $0 FROM t", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $0", Position: "8"}},
				{"SELECT $2 FROM t", fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}},
				{"SELECT $1 FROM t WHERE v=$1 ORDER BY id", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
				{"SELECT $1 FROM t WHERE f=$1 ORDER BY id", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
			} {
				step := fixture.ImplicitError(tc.q, tc.want)
				step.SelectRows, step.ErrorStage = true, "execute"
				// With no actuals pq uses the simple-query path; native still
				// finalizes its typed frame. Both contracts are independently
				// specified in TestReferenceQueryCoreDiagnosticsAndParameterBoundary.
				if backend.PG {
					parameter := ""
					if tc.want.Code == "42P18" {
						parameter = "$2"
					}
					if tc.want.Code == "42P08" {
						parameter = "$1"
					}
					if parameter != "" {
						step = fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter " + parameter, Position: "8"})
						step.SelectRows, step.ErrorStage = true, "execute"
					}
				}
				run(step)
				step = fixture.ImplicitError(tc.q, tc.want)
				step.SelectRows, step.ErrorStage = true, "prepare"
				run(fixture.Prepared(step))
			}
			rows, err := held.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("missing actual argument exposed rows")
			}
			fixture.TransactionOrdinary(t, err, "sql: expected 1 arguments, got 0")
			// Failed later tuples must not publish the successful first tuple.
			exec("CREATE TABLE atomic(i INT NOT NULL)", 0)
			exec("INSERT INTO atomic VALUES(99)", 1)
			for _, step := range []fixture.Step{
				fixture.ImplicitError("INSERT INTO atomic VALUES(1),('oops')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "31"}),
				fixture.ImplicitError("INSERT INTO atomic VALUES(1),(2147483648)", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}),
				fixture.ImplicitError("INSERT INTO atomic VALUES(1),(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "atomic" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "atomic", Column: "i"}),
			} {
				step.ErrorStage = "execute"
				run(step)
				run(fixture.Select("SELECT i,7 FROM atomic", []string{"i", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(7)}}))
			}
			if backend.PG {
				return
			}
			// Raw cursor capture precedes any fetching or control replacement.
			raw, err := p.NativeConnector.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			owned := []byte("owned")
			cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,$1,NULL FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			owned[0] = 'X'
			exec("INSERT INTO t VALUES(5,5,5,'later',TRUE)", 1)
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
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('new')")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cursor.Columns(), []string{"id", "?column?", "?column?"}) {
				t.Fatal(cursor.Columns())
			}
			meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
			for i, want := range []string{"INT4", "TEXT", "TEXT"} {
				if meta.ColumnTypeDatabaseTypeName(i) != want {
					t.Fatal("retained metadata changed")
				}
			}
			dst := make([]driver.Value, 3)
			for i := range 4 {
				if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(i + 1), "owned", nil}) {
					t.Fatal(dst, err)
				}
			}
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{"new"}}))
			// Restore replaced the entire public descriptor set; establish the
			// original sentinel again for the parameterized-replay rejection.
			exec("CREATE TABLE atomic(i INT NOT NULL)", 0)
			exec("INSERT INTO atomic VALUES(99)", 1)
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)")); err == nil {
				t.Fatal("parameter restore accepted")
			}
			run(fixture.Select("SELECT i FROM atomic", []string{"i"}, []string{"INT4"}, [][]any{{int64(99)}}))
		}()
	}
}
