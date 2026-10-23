package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryMeasureScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

type geometryMeasureCase struct {
	name, function, formal, input, typ string
	want                               any
}

var geometryMeasureCases = []geometryMeasureCase{
	{"area_box", "area", "box", "(2,2),(0,0)", "FLOAT8", float64(4)},
	{"area_path", "area", "path", "((0,0),(4,0),(0,3))", "FLOAT8", float64(6)},
	{"area_circle", "area", "circle", "<(3,-4),2>", "FLOAT8", float64(12.566370614359172)},
	{"center_box", "center", "box", "(1,2),(0,0)", "POINT", "(0.5,1)"},
	{"center_circle", "center", "circle", "<(3,-4),2>", "POINT", "(3,-4)"},
	{"diameter_circle", "diameter", "circle", "<(3,-4),2>", "FLOAT8", float64(4)},
	{"radius_circle", "radius", "circle", "<(3,-4),2>", "FLOAT8", float64(2)},
}

func geometryMeasureSelect(b fixture.Backend, q string, names, types []string, rows [][]any, args ...any) fixture.Step {
	s := geometryConversionSelect(b, q, names, types, rows, args...)
	geometryMetadata, geometryRaw := s.CheckMetadata, s.CheckRaw
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		geometryMetadata(t, rows)
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, col := range cols {
			if types[i] != "FLOAT8" {
				continue
			}
			if col.ScanType() != reflect.TypeFor[float64]() {
				t.Fatalf("%s ScanType %v want float64", names[i], col.ScanType())
			}
			if n, ok := col.Length(); n != 0 || ok {
				t.Fatalf("%s length %d/%t", names[i], n, ok)
			}
			if p, s, ok := col.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("%s decimal %d,%d/%t", names[i], p, s, ok)
			}
		}
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "FLOAT8" {
			return geometryRaw(t, column, row, value)
		}
		if value != nil {
			if _, ok := value.(float64); !ok {
				t.Fatalf("raw FLOAT8 %T %#v", value, value)
			}
		}
		return false
	}
	return s
}
