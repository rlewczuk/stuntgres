package reference_test

import (
	"database/sql"
	"reflect"
	"slices"
	"testing"

	"stuntgres/reference/fixture"
)

// Attribute scenarios share the existing fixture and procedure transport. This
// adds the declaration types absent from the procedure-output-only checker.
func attributesStep(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, values, args...)
	s.RawTypes = append([]string(nil), types...)
	s.CheckMetadata = attributesMetadata(b, names, types)
	return s
}

func attributesMetadata(b fixture.Backend, names, types []string, native ...resultsNativeColumn) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.Columns()
		if err != nil || !slices.Equal(columns, names) {
			t.Fatalf("columns %v %v want %v", columns, err, names)
		}
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != len(names) {
			t.Fatal(cols, err)
		}
		for i, c := range cols {
			scan := reflect.TypeFor[string]()
			switch types[i] {
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "FLOAT4":
				scan = reflect.TypeFor[float64]()
				if b.PG {
					scan = reflect.TypeFor[float32]()
				}
			case "FLOAT8":
				scan = reflect.TypeFor[float64]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "TEXT":
			case "CHAR", "_TEXT", "_INT4", "_CHAR", "RECORD":
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			default:
				t.Fatalf("unspecified attribute metadata type %q", types[i])
			}
			length, lengthOK := c.Length()
			precision, scale, decimalOK := c.DecimalSize()
			nullable, nullableOK := c.Nullable()
			wantLength, wantLengthOK := int64(0), false
			if types[i] == "TEXT" {
				wantLength, wantLengthOK = resultsTextMetadata.Length, true
			}
			wantNullable, wantNullableOK := false, false
			if !b.PG && len(native) != 0 {
				if len(native) != len(types) {
					t.Fatal("incomplete attribute metadata profile")
				}
				wantLength, wantLengthOK = native[i].Length, native[i].LengthKnown
				wantNullable, wantNullableOK = native[i].Nullable, native[i].NullableKnown
			}
			if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != 0 || scale != 0 || decimalOK || nullable != wantNullable || nullableOK != wantNullableOK {
				t.Errorf("attribute column %d: name=%s type=%s scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t; want %s/%s scan=%v length=%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, names[i], types[i], scan, wantLength, wantLengthOK, wantNullable, wantNullableOK)
			}
		}
	}
}

func attributesBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, rows [][]any, args ...any) {
	t.Helper()
	s := attributesStep(b, q, names, types, rows, args...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func attributesTextBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q, value string) {
	t.Helper()
	attributesBoth(t, p, b, c, q, []string{"v"}, []string{"TEXT"}, [][]any{{value}})
}

func attributesSettingBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q, value string) {
	t.Helper()
	profile := resultsTextMetadata
	profile.Nullable, profile.NullableKnown = true, true
	attributesBothMetadata(t, p, b, c, q, []string{"v"}, []string{"TEXT"}, [][]any{{value}}, []resultsNativeColumn{profile})
}

func attributesBothMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, rows [][]any, native []resultsNativeColumn, args ...any) {
	t.Helper()
	s := attributesStep(b, q, names, types, rows, args...)
	s.CheckMetadata = attributesMetadata(b, names, types, native...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func attributesIntBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, value int64) {
	t.Helper()
	attributesBoth(t, p, b, c, q, []string{"v"}, []string{"INT4"}, [][]any{{value}})
}

func attributesQueryError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, want fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want)
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
