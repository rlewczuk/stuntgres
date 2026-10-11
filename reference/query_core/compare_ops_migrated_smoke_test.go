package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSmokeCompareOpsSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, ctx := backend.DB, p.Ctx
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, db, backend.Recorder, s) }
			selectStep := func(q string, types []string, want [][]any, args ...any) fixture.Step {
				names := make([]string, len(types))
				for i := range names {
					names[i] = "?column?"
					if types[i] == "INT4" {
						names[i] = "id"
					}
				}
				s := fixture.Select(q, names, types, want, args...)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					meta, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.ScalarAssertMetadata(t, backend.PG, meta)
				}
				return s
			}
			run(fixture.Exec("CREATE TABLE t(id INTEGER,i INTEGER,j BIGINT,s TEXT,b BOOLEAN)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,2147483647,2147483648,'a',FALSE),(2,-2147483648,-2147483649,'z',TRUE),(3,NULL,NULL,NULL,NULL),(4,7,7,'a',FALSE)", 4))
			for _, tc := range []struct {
				op     string
				values []any
				ids    []int64
			}{
				{"=", []any{false, false, nil, true}, []int64{4}}, {"<>", []any{true, true, nil, false}, []int64{1, 2}}, {"!=", []any{true, true, nil, false}, []int64{1, 2}},
				{"<", []any{true, false, nil, false}, []int64{1}}, {"<=", []any{true, false, nil, true}, []int64{1, 4}}, {">", []any{false, true, nil, false}, []int64{2}}, {">=", []any{false, true, nil, true}, []int64{2, 4}},
			} {
				want := [][]any{}
				for _, v := range tc.values {
					want = append(want, []any{v})
				}
				run(selectStep("SELECT i"+tc.op+"j FROM t ORDER BY id", []string{"BOOL"}, want))
				want = [][]any{}
				for _, id := range tc.ids {
					want = append(want, []any{id})
				}
				run(selectStep("SELECT id FROM t WHERE i"+tc.op+"j ORDER BY id", []string{"INT4"}, want))
				run(selectStep("SELECT NULL"+tc.op+"NULL FROM t", []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}}))
			}
			for _, tc := range []struct {
				q     string
				types []string
				want  [][]any
				args  []any
			}{
				{"SELECT -9223372036854775808<-2147483648,9223372036854775807>2147483647,9007199254740993>9007199254740992 FROM t", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {true, true, true}, {true, true, true}, {true, true, true}}, nil},
				{"SELECT 'a'<'z',FALSE<TRUE,NULL<'x' FROM t", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, nil}, {true, true, nil}, {true, true, nil}, {true, true, nil}}, nil},
				{"SELECT i<j FROM t WHERE NULL<>NULL", []string{"BOOL"}, [][]any{}, nil},
				{"SELECT $1<$2 FROM t", []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, []any{"10", "2"}},
				{"SELECT i=$1,j=$1 FROM t ORDER BY id", []string{"BOOL", "BOOL"}, [][]any{{false, false}, {false, false}, {nil, nil}, {true, true}}, []any{7}},
			} {
				s := selectStep(tc.q, tc.types, tc.want, tc.args...)
				run(s)
				run(fixture.Prepared(s))
			}
			const q = "SELECT $1<i FROM t ORDER BY id"
			stmt, err := db.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}
			for _, arg := range []any{7, nil, 7} {
				want := [][]any{{true}, {false}, {nil}, {false}}
				if arg == nil {
					want = [][]any{{nil}, {nil}, {nil}, {nil}}
				}
				fixture.RunStep(t, ctx, held, backend.Recorder, selectStep(q, []string{"BOOL"}, want, arg))
			}
			const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
				args []any
			}{
				{"SELECT i<TRUE FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "9", Hint: hint}, nil},
				{"SELECT s!=b FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: text <> boolean", Position: "9", Hint: hint}, nil},
				{"SELECT i<'oops' FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "10"}, nil},
				{"SELECT $1,i=$1 FROM t", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Position: "8", Detail: "integer versus text"}, []any{7}},
				{"SELECT i=$1,b=$1 FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: boolean = integer", Position: "14", Hint: hint}, []any{7}},
			} {
				s := fixture.ImplicitError(tc.q, tc.want, tc.args...)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				s.ErrorStage = "prepare"
				run(fixture.Prepared(s))
			}
			bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops")
			bad.SelectRows = true
			bad.ErrorStage = "execute"
			fixture.RunStep(t, ctx, held, backend.Recorder, bad)
			if !backend.PG {
				raw, err := p.NativeConnector.Connect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT i<j FROM t ORDER BY id", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cursor.Close()
				run(fixture.Exec("INSERT INTO t VALUES(5,9,10,'later',TRUE)", 1))
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
				if !reflect.DeepEqual(cursor.Columns(), []string{"?column?"}) || cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "BOOL" {
					t.Fatal("captured metadata changed")
				}
				dst := make([]driver.Value, 1)
				for _, want := range []driver.Value{true, false, nil, false} {
					if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{want}) {
						t.Fatal(dst, want, err)
					}
				}
				if err := cursor.Next(dst); err != io.EOF {
					t.Fatal(err)
				}
				run(selectStep("SELECT id FROM t WHERE i<j ORDER BY id", []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}}))
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := db.QueryContext(canceled, "SELECT i<j FROM t"); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
}
