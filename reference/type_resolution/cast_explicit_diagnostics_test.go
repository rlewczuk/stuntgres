package reference_test

import (
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastExplicitDiagnosticsPhases(t *testing.T) {
	p := castReferenceSetup(t)
	cases := []struct {
		q        string
		d        fixture.Diagnostic
		analysis bool
	}{
		{"SELECT CAST(n AS bool) FROM t", fixture.Diagnostic{Code: "42846", Message: "cannot cast type bigint to boolean", Position: "8"}, true},
		{"SELECT n::bool FROM t", fixture.Diagnostic{Code: "42846", Message: "cannot cast type bigint to boolean", Position: "9"}, true},
		{"SELECT CAST(b AS bigint) FROM t", fixture.Diagnostic{Code: "42846", Message: "cannot cast type boolean to bigint", Position: "8"}, true},
		{"SELECT bool(n) FROM t", fixture.Diagnostic{Code: "42883", Message: "function bool(bigint) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT int8(b) FROM t", fixture.Diagnostic{Code: "42883", Message: "function int8(boolean) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true},
		{"SELECT CAST('oops' AS integer) FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "13"}, true},
		{"SELECT int4('oops') FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "13"}, true},
		{"SELECT /*é*/ 'oops'::int4 FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "14"}, true},
		{"SELECT CAST('truth' AS boolean) FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "13"}, true},
		{"SELECT '2147483648'::int4 FROM t", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "8"}, true},
		{"SELECT CAST('9223372036854775808' AS bigint) FROM t", fixture.Diagnostic{Code: "22003", Message: `value "9223372036854775808" is out of range for type bigint`, Position: "13"}, true},
		{"SELECT 2147483648::int4 FROM t", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, false},
		{"SELECT (-2147483649)::int4 FROM t", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, false},
		{"SELECT CAST('oops'::text AS int4) FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false},
		{"SELECT s::int4 FROM t ORDER BY id", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "no"`}, false},
		{"SELECT s::bool FROM t ORDER BY id", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "  +42 "`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			step := castReferenceError(tc.q, tc.d, false)
			fixture.ScalarStep(t, p, step)
			if tc.analysis {
				step.ErrorStage = "prepare"
				fixture.ScalarStep(t, p, fixture.Prepared(step))
			} else {
				step.ErrorStage = "execute"
				fixture.ScalarStep(t, p, fixture.Prepared(step))
			}
		})
	}
	for _, tc := range []struct {
		name string
		pos  string
	}{{"integer", "15"}, {"bigint", "14"}, {"boolean", "15"}, {"int", "11"}} {
		fixture.ScalarStep(t, p, castReferenceError("SELECT "+tc.name+"('42') FROM t", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "("`, Position: tc.pos}, false))
	}
	fixture.ScalarStep(t, p, castReferenceError(`SELECT "integer"('42') FROM t`, fixture.Diagnostic{Code: "42883", Message: "function integer(unknown) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, false))
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE empty(n INT8,b BOOL)", 0))
	for _, q := range []string{"SELECT n::bool FROM empty", "SELECT NULL::int8::bool FROM empty"} {
		pos := "9"
		if strings.Contains(q, "NULL") {
			pos = "18"
		}
		step := castReferenceError(q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type bigint to boolean", Position: pos}, false)
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}
func TestReferenceCastExplicitParameters(t *testing.T) {
	p := castReferenceSetup(t)
	for _, tc := range []struct {
		q            string
		names, types []string
		arg          any
		row          []any
	}{{"SELECT $1::int4,$1::text FROM t ORDER BY id", []string{"int4", "text"}, []string{"INT4", "TEXT"}, 2, []any{int64(2), "2"}}, {"SELECT $1::int4,$1::int8 FROM t ORDER BY id", []string{"int4", "int8"}, []string{"INT4", "INT8"}, 2, []any{int64(2), int64(2)}}, {"SELECT text($1) FROM t ORDER BY id", []string{"text"}, []string{"TEXT"}, "42", []any{"42"}}, {"SELECT int4($1::text) FROM t ORDER BY id", []string{"int4"}, []string{"INT4"}, "42", []any{int64(42)}}} {
		for _, arg := range []any{tc.arg, nil} {
			row := tc.row
			if arg == nil {
				row = make([]any, len(tc.row))
			}
			step := fixture.Select(tc.q, tc.names, tc.types, [][]any{row, row, row}, arg)
			fixture.ScalarStep(t, p, step)
			fixture.ScalarStep(t, p, fixture.Prepared(step))
		}
		for _, backend := range p.Backends() {
			held, err := backend.DB.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, arg := range []any{tc.arg, nil, tc.arg} {
				row := tc.row
				if arg == nil {
					row = make([]any, len(tc.row))
				}
				fixture.ScalarRunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, held, tc.q}, backend.Recorder, fixture.Select(tc.q, tc.names, tc.types, [][]any{row, row, row}, arg))
			}
			held.Close()
		}

	}
	for _, name := range []string{"int4", "int8", "bool"} {
		step := castReferenceError("SELECT "+name+"($1) FROM t", fixture.Diagnostic{Code: "42725", Message: "function " + name + "(unknown) is not unique", Position: "8", Hint: "Could not choose a best candidate function. You might need to add explicit type casts."}, false, "42")
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	step := castReferenceError("SELECT $1,$1::int4 FROM t", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, false, 2)
	fixture.ScalarStep(t, p, step)
	step.ErrorStage = "prepare"
	fixture.ScalarStep(t, p, fixture.Prepared(step))
	for _, q := range []string{"SELECT $1::int4 FROM t", "SELECT int4($1::text) FROM t"} {
		step := castReferenceError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false, "oops")
		if strings.Contains(q, "$1::int4") {
			pg := *step.Diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			step.Diagnostic = &pg
		}
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"-held", func(t *testing.T) {
			stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT CAST($1 AS bool) FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, arg := range []any{true, false, nil, true} {
				rows, err := stmt.QueryContext(p.Ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "BOOL" {
					t.Fatal(meta, err)
				}
				fixture.ScalarAssertMetadata(t, backend.PG, meta)
				n := 0
				for rows.Next() {
					var got any
					if err := rows.Scan(&got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, arg) {
						t.Fatal(got, arg)
					}
					n++
				}
				if n != 3 || rows.Err() != nil {
					t.Fatal(n, rows.Err())
				}
				rows.Close()
			}
		})
	}
}
func TestReferenceCastExplicitAtomicity(t *testing.T) {
	p := castReferenceSetup(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE atom(i INT4)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO atom VALUES(7)", 1))
	for _, tc := range []struct {
		q        string
		args     []any
		d        fixture.Diagnostic
		analysis bool
	}{{"INSERT INTO atom VALUES(CAST('8' AS int4)),(CAST('oops' AS int4))", nil, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "50"}, true}, {"INSERT INTO atom VALUES(8),(2147483648::int4)", nil, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}, false}, {"INSERT INTO atom VALUES(int4($1::text)),(int4($2::text))", []any{"8", "oops"}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false}} {
		step := fixture.ImplicitError(tc.q, tc.d, tc.args...)
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		if tc.analysis {
			step.ErrorStage = "prepare"
			fixture.ScalarStep(t, p, fixture.Prepared(step))
		} else {
			if p.Postgres != nil {
				t.Run("postgres-prepared", func(t *testing.T) {
					fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
				})
			}
		}
		fixture.ScalarStep(t, p, fixture.Select("SELECT i FROM atom", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}
