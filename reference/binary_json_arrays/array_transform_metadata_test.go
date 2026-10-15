package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayTransformMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows, names, types []string) []*sql.ColumnType {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.ScalarAssertDescription(t, cols, names, types)
	for i, col := range cols {
		if nullable, known := col.Nullable(); nullable || known {
			t.Fatalf("computed %s Nullable=%t/%t", col.Name(), nullable, known)
		}
		want := reflect.TypeFor[string]()
		if b.PG && strings.HasPrefix(types[i], "_") {
			want = reflect.TypeFor[any]()
		}
		if col.ScanType() != want {
			t.Fatalf("%s ScanType=%v want %v", col.Name(), col.ScanType(), want)
		}
		if strings.HasPrefix(types[i], "_") {
			if n, known := col.Length(); n != 0 || known {
				t.Fatalf("array Length=%d/%t", n, known)
			}
			if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatalf("array DecimalSize=%d/%d/%t", precision, scale, known)
			}
		}
	}
	return cols
}

func arrayTransformHeldTrim(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT trim_array($1::int[],$2::int) AS a FROM at_one"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		input any
		count any
		want  any
		bad   bool
	}{
		{"[-1:1][5:6]={{1,2},{3,NULL},{5,6}}", int64(1), "{{1,2},{3,NULL}}", false},
		{"[-1:1][5:6]={{1,2},{3,NULL},{5,6}}", int64(4), nil, true},
		{nil, int64(1), nil, false},
		{"[-1:1][5:6]={{1,2},{3,NULL},{5,6}}", int64(0), "{{1,2},{3,NULL},{5,6}}", false},
	} {
		if tc.bad {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2202E", Message: "number of elements to trim must be between 0 and 3"}, tc.input, tc.count)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			continue
		}
		s := arrayTransformSelect(b, q, []string{"a"}, []string{"_INT4"}, [][]any{{tc.want}}, tc.input, tc.count)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { arrayTransformMetadata(t, b, rows, s.Columns, s.Types) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceArrayTransformHeldMetadata(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayTransformHeldTrim(t, p, b, c)
		for _, tc := range []struct {
			name, expression, typ string
			args                  []any
			want                  any
		}{
			{"append", "array_append($1::int[],$2::int)", "_INT4", []any{"[0:1]={1,2}", int64(3)}, "[0:2]={1,2,3}"},
			{"prepend", "array_prepend($2::int,$1::int[])", "_INT4", []any{"[0:1]={1,2}", int64(3)}, "[0:2]={3,1,2}"},
			{"cat", "array_cat($1::int[],$2::int[])", "_INT4", []any{"{1}", "{2}"}, "{1,2}"},
			{"fill", "array_fill($1::int,$2::int[])", "_INT4", []any{int64(7), "{2}"}, "{7,7}"},
			{"reverse", "array_reverse($1::int[])", "_INT4", []any{"{1,2}"}, "{2,1}"},
			{"sort", "array_sort($1::int[],$2::bool,$3::bool)", "_INT4", []any{"{1,NULL,2}", true, false}, "{2,1,NULL}"},
			{"string", "array_to_string($1::int[],$2::text,$3::text)", "TEXT", []any{"{1,NULL,2}", ",", "*"}, "1,*,2"},
			{"operator", "$1::int[] || $2::int", "_INT4", []any{"{1}", int64(2)}, "{1,2}"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.expression + " AS a FROM at_one"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for range 2 {
					s := arrayTransformSelect(b, q, []string{"a"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...)
					var retained []*sql.ColumnType
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { retained = arrayTransformMetadata(t, b, rows, s.Columns, s.Types) }
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.ScalarAssertDescription(t, retained, s.Columns, s.Types)
				}
				// A malformed parameter must not poison this SAME statement.
				// Full portal Context follows existing array input diagnostics.
				badArgs := append([]any(nil), tc.args...)
				arrayIndex := 0
				if tc.name == "fill" {
					arrayIndex = 1
				}
				badArgs[arrayIndex] = "oops"
				d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "oops"`)
				d.Detail = `Array value must start with "{" or dimension information.`
				d.Context = "unnamed portal parameter $1 = '...'"
				if tc.name == "fill" {
					d.Context = "unnamed portal parameter $2 = '...'"
				}
				bad := fixture.ErrorDiagnostic(q, d, badArgs...)
				bad.SelectRows, bad.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
				// Whole-NULL propagation differs for nonstrict append/prepend/fill.
				nullArgs := append([]any(nil), tc.args...)
				nullArgs[0] = nil
				var nullWant any
				switch tc.name {
				case "append", "operator":
					nullWant = "{3}"
				case "prepend":
					nullWant = "{3}"
				case "cat":
					nullWant = "{2}"
				case "fill":
					nullWant = "{NULL,NULL}"
				}
				if tc.name == "operator" {
					nullWant = "{2}"
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, arrayTransformSelect(b, q, []string{"a"}, []string{tc.typ}, [][]any{{nullWant}}, nullArgs...))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, arrayTransformSelect(b, q, []string{"a"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...))
				for _, suffix := range []string{" WHERE FALSE", " WHERE id=1"} {
					args := append([]any(nil), tc.args...)
					args[0] = nil
					want := [][]any{{nullWant}}
					if suffix == " WHERE FALSE" {
						want = nil
					}
					s := arrayTransformSelect(b, q+suffix, []string{"a"}, []string{tc.typ}, want, args...)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { arrayTransformMetadata(t, b, rows, s.Columns, s.Types) }
					arrayTransformBoth(t, p, b, c, s)
				}
				arrayTransformAssertContract(t, p, b, c, tc.name+"_all_accessors", q, tc.args...)
			})
		}
	})
}
