package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Source-column declarations, not observed rows or output-name heuristics,
// supply native nullability. Computed outputs and pq nullable metadata are unknown.
type jsonTypeSourceColumn struct {
	index    int
	nullable bool
}

func jsonTypeMetadataCheck(t *testing.T, rows *sql.Rows, pg bool, names, types []string, sourceColumns ...jsonTypeSourceColumn) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatalf("columns=%v want=%v error=%v", columns, names, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatalf("metadata=%v error=%v", metadata, err)
	}
	for i, ct := range metadata {
		wantScan := reflect.TypeFor[string]()
		if types[i] == "INT8" {
			wantScan = reflect.TypeFor[int64]()
		}
		if types[i] == "INT4" {
			wantScan = reflect.TypeFor[int64]()
			if pg {
				wantScan = reflect.TypeFor[int32]()
			}
		}
		if ct.Name() != names[i] || ct.DatabaseTypeName() != types[i] || ct.ScanType() != wantScan {
			t.Fatalf("column%d=%q/%q/%v want=%q/%q/%v", i, ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), names[i], types[i], wantScan)
		}
		wantLength, wantLengthKnown := int64(0), false
		if types[i] == "TEXT" {
			// Native Usage.Length and independently observed pinned pq metadata
			// both advertise unbounded TEXT as MaxInt64, including empty sets.
			wantLength, wantLengthKnown = 9223372036854775807, true
		}
		if length, known := ct.Length(); length != wantLength || known != wantLengthKnown {
			t.Fatalf("column%d Length=%d,%v want=%d,%v", i, length, known, wantLength, wantLengthKnown)
		}
		if precision, scale, known := ct.DecimalSize(); known {
			t.Fatalf("column%d unexpected decimal %d,%d", i, precision, scale)
		}
		wantNullable, wantNullabilityKnown := false, false
		if !pg {
			for _, column := range sourceColumns {
				if column.index == i {
					wantNullable, wantNullabilityKnown = column.nullable, true
				}
			}
		}
		if nullable, known := ct.Nullable(); nullable != wantNullable || known != wantNullabilityKnown {
			t.Fatalf("column%d Nullable=%v,%v want=%v,%v", i, nullable, known, wantNullable, wantNullabilityKnown)
		}
	}
}

// Each actual reader gets its own backend expectation, never a retained native
// description reused as the expected PostgreSQL replay metadata.
func jsonTypeWithMetadata(step fixture.Step, pg bool, sourceColumns ...jsonTypeSourceColumn) fixture.Step {
	names, types := step.Columns, step.Types
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		jsonTypeMetadataCheck(t, rows, pg, names, types, sourceColumns...)
	}
	return step
}

func jsonTypeMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT "+family+"_typeof('{}'), "+family+"_array_length('[]')", []string{family + "_typeof", family + "_array_length"}, []string{"TEXT", "INT4"}, [][]any{{"object", int64(0)}}),
			fixture.Select("SELECT "+family+"_typeof(NULL) AS kind, "+family+"_array_length(NULL) AS size", []string{"kind", "size"}, []string{"TEXT", "INT4"}, [][]any{{nil, nil}}),
			fixture.Select("SELECT * FROM "+family+"_object_keys('{}')", []string{family + "_object_keys"}, []string{"TEXT"}, [][]any{}),
			fixture.Select("SELECT "+family+"_object_keys(NULL) AS empty", []string{"empty"}, []string{"TEXT"}, [][]any{}),
			jsonTypeKeySelect(family, `{"a":1}`, [][]any{{"a", int64(1)}}),
			jsonTypeKeySelect(family, nil, [][]any{}),
		} {
			names, types := s.Columns, s.Types
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonTypeMetadataCheck(t, rows, b.PG, names, types) }
			s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
				raw := make([]any, len(types))
				dest := make([]any, len(types))
				for i := range raw {
					dest[i] = &raw[i]
				}
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				for i, value := range raw {
					fixture.AssertRawValue(t, b.PG, types[i], value)
				}
			}
			jsonTypeBoth(t, p, b, c, s)
		}
	}
}

func TestReferenceJSONTypeMetadata(t *testing.T) { jsonTypeScenario(t, jsonTypeMetadata) }
