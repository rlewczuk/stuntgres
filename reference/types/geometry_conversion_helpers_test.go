package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryConversionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=3", 0))
			body(t, p, b, c)
		})
	}
}

func geometryConversionSelect(b fixture.Backend, q string, names, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, rows, args...)
	isGeometry := func(name string) bool { return name == "BOX" || name == "CIRCLE" || name == "LINE" }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if !isGeometry(types[column]) {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("raw cell %d,%d is %T, want %v", row, column, value, want)
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != len(types) {
			t.Fatalf("metadata width %d want %d", len(cols), len(types))
		}
		for i, col := range cols {
			if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
				t.Fatalf("column %d: %s/%s want %s/%s", i, col.Name(), col.DatabaseTypeName(), names[i], types[i])
			}
			if !isGeometry(types[i]) {
				continue
			}
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[any]()
			}
			if col.ScanType() != want {
				t.Fatalf("ScanType %v want %v", col.ScanType(), want)
			}
			if n, ok := col.Length(); n != 0 || ok {
				t.Fatalf("length %d/%t", n, ok)
			}
			if p, s, ok := col.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("decimal %d,%d/%t", p, s, ok)
			}
		}
	}
	return s
}

type geometryConversionCase struct {
	name, expression, parameter, typ, want string
	args                                   []any
}

var geometryConversionCases = []geometryConversionCase{
	{"box_circle", "box('<(0,0),2>'::circle)", "box($1::circle)", "BOX", "(1.414213562373095,1.414213562373095),(-1.414213562373095,-1.414213562373095)", []any{"<(0,0),2>"}},
	{"box_point", "box(point '(1,0)')", "box($1::point)", "BOX", "(1,0),(1,0)", []any{"(1,0)"}},
	{"box_points", "box(point '(0,1)',point '(1,0)')", "box($1::point,$2::point)", "BOX", "(1,1),(0,0)", []any{"(0,1)", "(1,0)"}},
	{"box_polygon", "box(polygon '((0,0),(1,1),(2,0))')", "box($1::polygon)", "BOX", "(2,1),(0,0)", []any{"((0,0),(1,1),(2,0))"}},
	{"bound_box", "bound_box(box '(1,1),(0,0)',box '(4,4),(3,3)')", "bound_box($1::box,$2::box)", "BOX", "(4,4),(0,0)", []any{"(1,1),(0,0)", "(4,4),(3,3)"}},
	{"circle_box", "circle(box '(6,8),(0,0)')", "circle($1::box)", "CIRCLE", "<(3,4),5>", []any{"(6,8),(0,0)"}},
	{"circle_point", "circle(point '(0,0)',2.0)", "circle($1::point,$2::float8)", "CIRCLE", "<(0,0),2>", []any{"(0,0)", float64(2)}},
	{"circle_polygon", "circle(polygon '((0,0),(1,3),(2,0))')", "circle($1::polygon)", "CIRCLE", "<(1,1),1.6094757082487299>", []any{"((0,0),(1,3),(2,0))"}},
	{"line_points", "line(point '(-1,0)',point '(1,0)')", "line($1::point,$2::point)", "LINE", "{0,-1,0}", []any{"(-1,0)", "(1,0)"}},
}
