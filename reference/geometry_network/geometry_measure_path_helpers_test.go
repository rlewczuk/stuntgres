package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryMeasurePathScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

var geometryMeasurePathCases = []geometryMeasureCase{
	{"closed", "isclosed", "path", "[(0,0),(4,0),(0,3)]", "BOOL", false},
	{"open", "isopen", "path", "[(0,0),(4,0),(0,3)]", "BOOL", true},
	{"close", "pclose", "path", "[(0,0),(4,0),(0,3)]", "PATH", "((0,0),(4,0),(0,3))"},
	{"reopen", "popen", "path", "((0,0),(4,0),(0,3))", "PATH", "[(0,0),(4,0),(0,3)]"},
	{"length_path", "length", "path", "[(0,0),(4,0),(0,3)]", "FLOAT8", float64(9)},
	{"length_lseg", "length", "lseg", "[(0,0),(3,4)]", "FLOAT8", float64(5)},
	{"count_path", "npoints", "path", "[(0,0),(4,0),(0,3)]", "INT4", int64(3)},
	{"count_polygon", "npoints", "polygon", "((0,0),(4,0),(0,3),(0,0))", "INT4", int64(4)},
}

func geometryMeasurePathSelect(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	return geometryMeasurePathSelectMetadata(b, q, names, types, values, false, args...)
}

// Stored table and view columns expose native nullability provenance; computed
// projections do not. lib/pq does not expose nullability for either result.
func geometryMeasurePathStoredSelect(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	return geometryMeasurePathSelectMetadata(b, q, names, types, values, true, args...)
}

func geometryMeasurePathSelectMetadata(b fixture.Backend, q string, names, types []string, values [][]any, stored bool, args ...any) fixture.Step {
	s := geometryMeasureSelect(b, q, names, types, values, args...)
	metadata, raw := s.CheckMetadata, s.CheckRaw
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		metadata(t, rows)
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, col := range cols {
			var want reflect.Type
			switch types[i] {
			case "BOOL":
				want = reflect.TypeFor[bool]()
			case "INT4":
				want = reflect.TypeFor[int64]()
				if b.PG {
					want = reflect.TypeFor[int32]()
				}
			default:
				continue
			}
			if col.ScanType() != want {
				t.Fatalf("%s ScanType %v want %v", names[i], col.ScanType(), want)
			}
			if n, ok := col.Length(); n != 0 || ok {
				t.Fatalf("%s Length %d/%t", names[i], n, ok)
			}
			if p, s, ok := col.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("%s DecimalSize %d/%d/%t", names[i], p, s, ok)
			}
		}
		for _, col := range cols {
			wantNullable, wantKnown := stored && !b.PG, stored && !b.PG
			if nullable, known := col.Nullable(); nullable != wantNullable || known != wantKnown {
				t.Fatalf("%s Nullable %t/%t want %t/%t", col.Name(), nullable, known, wantNullable, wantKnown)
			}
		}
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value != nil {
			switch types[column] {
			case "BOOL":
				if _, ok := value.(bool); !ok {
					t.Fatalf("raw BOOL %T", value)
				}
			case "INT4":
				if _, ok := value.(int64); !ok {
					t.Fatalf("raw INT4 %T", value)
				}
			}
		}
		return raw(t, column, row, value)
	}
	return s
}
