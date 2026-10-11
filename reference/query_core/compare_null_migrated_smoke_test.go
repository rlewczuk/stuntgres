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

// TestReferenceSmokeCompareNullSmoke exercises the public driver, in both direct and prepared
// modes. Expected values are independent of the implementation under test.
func TestReferenceSmokeCompareNullSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, db := p.Ctx, backend.DB
			fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Exec("CREATE TABLE t(id INTEGER,v INTEGER,b BIGINT,s TEXT,f BOOLEAN)", 0))
			fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)", 4))
			cases := []struct {
				name, query  string
				names, types []string
				want         [][]any
			}{
				{"null aliases", "SELECT id,v IS NULL,v IS NOT NULL,v ISNULL,v NOTNULL FROM t ORDER BY id",
					[]string{"id", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"},
					[][]any{{int64(1), false, true, false, true}, {int64(2), true, false, true, false}, {int64(3), false, true, false, true}, {int64(4), false, true, false, true}}},
				{"truth matrix", "SELECT id,f IS TRUE,f IS NOT TRUE,f IS FALSE,f IS NOT FALSE,f IS UNKNOWN,f IS NOT UNKNOWN FROM t ORDER BY id",
					[]string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
					[][]any{{int64(1), true, false, false, true, false, true}, {int64(2), false, true, false, true, true, false}, {int64(3), false, true, true, false, false, true}, {int64(4), true, false, false, true, false, true}}},
				{"distinct filter", "SELECT id FROM t WHERE v IS DISTINCT FROM 1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}},
				{"not distinct filter", "SELECT id FROM t WHERE 1 IS NOT DISTINCT FROM v ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}},
				{"ordinary equality remains unknown", "SELECT id FROM t WHERE v=NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{}},
				{"unknown equality wrapper", "SELECT id FROM t WHERE (v=NULL) IS UNKNOWN ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
				{"grouped predicate", "SELECT (v IS NULL) IS TRUE FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {false}, {false}}},
				{"unary postfix chain", "SELECT TRUE IS NULL IS TRUE,v IS NULL ISNULL FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}, {false, false}, {false, false}, {false, false}}},
				{"unary postfix chain filter", "SELECT id FROM t WHERE TRUE IS NULL IS TRUE ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{}},
				{"comparison predicate projection", "SELECT (v=1) IS TRUE,(v=NULL) IS UNKNOWN,(v=v) IS TRUE FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {false, true, false}, {false, true, true}, {true, true, true}}},
				{"logical predicate projection", "SELECT v IS NULL OR f IS TRUE,NOT v IS NULL,(f AND TRUE) IS UNKNOWN FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}, {true, false, true}, {false, true, false}, {true, true, false}}},
				{"mixed precedence", "SELECT id FROM t WHERE v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(4)}}},
				{"grouped logical distinct", "SELECT id,(f OR FALSE) IS NOT TRUE,(NOT f) IS DISTINCT FROM FALSE FROM t ORDER BY id", []string{"id", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), false, false}, {int64(2), true, true}, {int64(3), true, true}, {int64(4), false, false}}},
				{"ordinary null projection", "SELECT v=NULL FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}}},
				{"grouped AND unknown filter", "SELECT id FROM t WHERE (f AND TRUE) IS UNKNOWN ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}},
				{"grouped OR truth filter", "SELECT id FROM t WHERE (f OR FALSE) IS NOT TRUE ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}},
				{"grouped logical distinct filter", "SELECT id FROM t WHERE (NOT f) IS DISTINCT FROM FALSE ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}},
				{"mixed column function roots", "SELECT num_nulls(v),v IS NULL FROM t ORDER BY id", []string{"num_nulls", "?column?"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), true}, {int64(0), false}, {int64(0), false}}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Run("direct", func(t *testing.T) {
						fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Select(tc.query, tc.names, tc.types, tc.want))
					})
					t.Run("prepared", func(t *testing.T) {
						stmt, err := db.PrepareContext(ctx, tc.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: tc.query}, backend.Recorder, fixture.Select(tc.query, tc.names, tc.types, tc.want))
					})
				})
			}
			t.Run("held null parameter", func(t *testing.T) {
				stmt, err := db.PrepareContext(ctx, "SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for i, arg := range []any{1, 2, nil} {
					want := [][][]any{{{int64(1)}, {int64(4)}}, {{int64(3)}}, {{int64(2)}}}[i]
					fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: "SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 ORDER BY id"}, backend.Recorder, fixture.Select("SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 ORDER BY id", []string{"id"}, []string{"INT4"}, want, arg))
				}
			})
			t.Run("held comparison logical predicate", func(t *testing.T) {
				q := "SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 AND (v=$1) IS NOT FALSE ORDER BY id"
				stmt, err := db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for i, arg := range []any{1, 2, nil} {
					want := [][][]any{{{int64(1)}, {int64(4)}}, {{int64(3)}}, {{int64(2)}}}[i]
					fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}, backend.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, want, arg))
					fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, want, arg))
				}
			})
			t.Run("truth filters", func(t *testing.T) {
				for i, variant := range []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"} {
					want := [][]any{}
					for _, id := range [][]int64{{1, 4}, {2, 3}, {3}, {1, 2, 4}, {2}, {1, 3, 4}}[i] {
						want = append(want, []any{id})
					}
					q := "SELECT id FROM t WHERE f " + variant + " ORDER BY id"
					fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, want))
					stmt, err := db.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}, backend.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, want))
					stmt.Close()
				}
			})
			t.Run("null distinct literals", func(t *testing.T) {
				q := "SELECT NULL IS DISTINCT FROM NULL,NULL IS NOT DISTINCT FROM NULL,NULL IS DISTINCT FROM 1,1 IS NOT DISTINCT FROM NULL FROM t"
				want := [][]any{{false, true, true, false}, {false, true, true, false}, {false, true, true, false}, {false, true, true, false}}
				names, types := []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}
				fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Select(q, names, types, want))
				stmt, err := db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}, backend.Recorder, fixture.Select(q, names, types, want))
			})
			t.Run("equality nil contrast", func(t *testing.T) {
				stmt, err := db.PrepareContext(ctx, "SELECT id FROM t WHERE v=$1")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: "SELECT id FROM t WHERE v=$1"}, backend.Recorder, fixture.Select("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, [][]any{}, nil))
			})
			t.Run("mixed ANY selected roots", func(t *testing.T) {
				q := "SELECT $1 IS TRUE,num_nulls($1),$1 IS NULL FROM t ORDER BY id"
				stmt, err := db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for i, arg := range []any{true, false, nil} {
					row := [][]any{{true, int64(0), false}, {false, int64(0), false}, {false, int64(1), true}}[i]
					want := [][]any{row, row, row, row}
					fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Select(q, []string{"?column?", "num_nulls", "?column?"}, []string{"BOOL", "INT4", "BOOL"}, want, arg))
					fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}, backend.Recorder, fixture.Select(q, []string{"?column?", "num_nulls", "?column?"}, []string{"BOOL", "INT4", "BOOL"}, want, arg))
				}
			})
			t.Run("mixed occurrence order diagnostics", func(t *testing.T) {
				for _, tc := range []struct {
					q, parameter, position string
				}{
					{"SELECT num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE FROM t", "$2", "18"},
					{"SELECT $1 IS NULL,num_nulls($2),$1 IS TRUE,$2 IS TRUE FROM t", "$1", "8"},
				} {
					want := stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter " + tc.parameter, Position: tc.position}
					rows, err := db.QueryContext(ctx, tc.q, true, true)
					if rows != nil {
						rows.Close()
						t.Fatal("mixed occurrence error exposed cursor")
					}
					{
						expected := fixture.MigrationDiagnostic(want)
						expected.Severity, expected.SeverityUnlocalized = "ERROR", "ERROR"
						fixture.AssertDiagnostic(t, err, &expected)
					}
					stmt, err := db.PrepareContext(ctx, tc.q)
					if stmt != nil {
						stmt.Close()
						t.Fatal("mixed occurrence error exposed handle")
					}
					{
						expected := fixture.MigrationDiagnostic(want)
						expected.Severity, expected.SeverityUnlocalized = "ERROR", "ERROR"
						fixture.AssertDiagnostic(t, err, &expected)
					}
				}
			})
			t.Run("full diagnostics", func(t *testing.T) {
				for _, tc := range []struct {
					q    string
					want stuntgres.Error
				}{
					{"SELECT 1 IS TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "8"}},
					{"SELECT v IS NULL AND 1 FROM t", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "22"}},
					{"SELECT TRUE OR 1 IS TRUE FROM t", stuntgres.Error{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "16"}},
					{"SELECT FALSE AND missing IS NULL FROM t", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}},
					{"SELECT v IS DISTINCT FROM TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
					{"SELECT 'truth' IS TRUE FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "8"}},
					{"SELECT $1 IS NULL FROM t", stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $1"}},
					{"SELECT $1 IS NULL,$1 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}},
					{"SELECT $1 IS NULL,$1 IS TRUE,$2 IS NULL FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}},
					{"SELECT $2 IS NULL,$2 IS TRUE FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $2", Position: "8"}},
					{"SELECT $1 IS DISTINCT FROM NULL,$1 IS TRUE,$2 IS NULL FROM t", stuntgres.Error{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}},
					{"SELECT $1 IS TRUE,$2 IS NULL FROM t", stuntgres.Error{Code: "42P18", Message: "could not determine data type of parameter $2"}},
				} {
					directWant := tc.want
					if backend.PG && strings.HasPrefix(tc.q, "SELECT $") {
						directWant = stuntgres.Error{Code: "42P02", Message: "there is no parameter " + tc.q[7:9], Position: "8"}
					}
					rows, err := db.QueryContext(ctx, tc.q)
					if rows != nil {
						rows.Close()
						t.Fatal("failed query exposed rows")
					}
					{
						expected := fixture.MigrationDiagnostic(directWant)
						expected.Severity, expected.SeverityUnlocalized = "ERROR", "ERROR"
						fixture.AssertDiagnostic(t, err, &expected)
					}
					stmt, err := db.PrepareContext(ctx, tc.q)
					if stmt != nil {
						stmt.Close()
						t.Fatal("failed prepare exposed handle")
					}
					{
						expected := fixture.MigrationDiagnostic(tc.want)
						expected.Severity, expected.SeverityUnlocalized = "ERROR", "ERROR"
						fixture.AssertDiagnostic(t, err, &expected)
					}
				}
			})
			if !backend.PG {
				t.Run("retained computed cursor controls", func(t *testing.T) {
					raw, err := p.NativeConnector.Connect(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer raw.Close()
					cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v IS NULL,f IS UNKNOWN FROM t ORDER BY id", nil)
					if err != nil {
						t.Fatal(err)
					}
					defer cursor.Close()
					if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES(5,5,5,'later',TRUE)"); err != nil {
						t.Fatal(err)
					}
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
					if !reflect.DeepEqual(cursor.Columns(), []string{"?column?", "?column?"}) {
						t.Fatal(cursor.Columns())
					}
					metadata := cursor.(driver.RowsColumnTypeDatabaseTypeName)
					for i := range 2 {
						if metadata.ColumnTypeDatabaseTypeName(i) != "BOOL" {
							t.Fatal("retained BOOL metadata changed")
						}
					}
					dst := make([]driver.Value, 2)
					for _, want := range [][]driver.Value{{false, false}, {true, true}, {false, false}, {false, false}} {
						if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
							t.Fatal(dst, want, err)
						}
					}
					if err := cursor.Next(dst); err != io.EOF {
						t.Fatal(err)
					}
					fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Select("SELECT replacement IS NOT NULL FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
				})
			}
		})
	}
}
