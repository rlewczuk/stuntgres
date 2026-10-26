package reference_test

import (
	"database/sql"
	"reflect"
	"slices"
	"testing"

	"stuntgres/reference/fixture"
)

func sqlExecutionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

// Computed expressions have unknown nullability, precision and scale. TEXT's
// unbounded length and pq's INT4 scan type follow the existing driver contract.
// An explicit native profile retains source-owned nullability, such as settings.
func sqlExecutionMetadata(b fixture.Backend, names, types []string, nativeNullable ...bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		got, err := rows.Columns()
		if err != nil || !slices.Equal(got, names) {
			t.Fatalf("columns=%v err=%v want=%v", got, err, names)
		}
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != len(names) {
			t.Fatal(cols, err)
		}
		for i, col := range cols {
			scan := reflect.TypeFor[int64]()
			length, lengthOK := int64(0), false
			switch types[i] {
			case "INT4":
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
			case "TEXT":
				scan = reflect.TypeFor[string]()
				length, lengthOK = 9223372036854775807, true
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			default:
				t.Fatalf("unspecified metadata type %q", types[i])
			}
			wantNullable, wantNullableKnown := false, false
			if !b.PG && len(nativeNullable) != 0 {
				wantNullable, wantNullableKnown = nativeNullable[i], true
			}
			l, lok := col.Length()
			n, nok := col.Nullable()
			p, s, dok := col.DecimalSize()
			if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan || l != length || lok != lengthOK || n != wantNullable || nok != wantNullableKnown || p != 0 || s != 0 || dok {
				t.Errorf("column %d=%q/%q scan=%v length=%d/%t nullable=%t/%t decimal=%d,%d/%t; want %q/%q scan=%v length=%d/%t nullable=%t/%t unknown decimal", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), l, lok, n, nok, p, s, dok, names[i], types[i], scan, length, lengthOK, wantNullable, wantNullableKnown)
			}
		}
	}
}

func sqlExecutionStep(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, values, args...)
	s.RawTypes = append([]string(nil), types...)
	s.CheckMetadata = sqlExecutionMetadata(b, names, types)
	return s
}

func sqlExecutionBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, values [][]any, args ...any) {
	t.Helper()
	s := sqlExecutionStep(b, q, names, types, values, args...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func sqlExecutionValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q, typ string, value any, args ...any) {
	t.Helper()
	sqlExecutionBoth(t, p, b, c, q, []string{"v"}, []string{typ}, [][]any{{value}}, args...)
}

func sqlExecutionCount(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, table string, count int64) {
	t.Helper()
	s := fixture.Select("SELECT count(*) AS v FROM "+table, []string{"v"}, []string{"INT8"}, [][]any{{count}})
	s.RawTypes = []string{"INT8"}
	s.CheckMetadata = parametersMetadataNullability(b, "v", "INT8", false, !b.PG)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

// Expected diagnostics are source-derived from REL_18_6, not learned from native
// execution. Empty optional fields are asserted by the existing complete helper.
func sqlExecutionCallError(t *testing.T, p *fixture.Pair, c *sql.Conn, q, function, code, message string) {
	t.Helper()
	plpgsqlError(t, p, c, q, true, plpgsqlRuntimeDiagnostic(code, message, function, "CALL"))
}
