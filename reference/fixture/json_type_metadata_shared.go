package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

// Source-column declarations, not observed rows or output-name heuristics,
// supply native nullability. Computed outputs and pq nullable metadata are unknown.
type JSONTypeSourceColumn struct {
	Index    int
	Nullable bool
}

func JSONTypeMetadataCheck(t *testing.T, rows *sql.Rows, pg bool, names, types []string, sourceColumns ...JSONTypeSourceColumn) {
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
				if column.Index == i {
					wantNullable, wantNullabilityKnown = column.Nullable, true
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
func JSONTypeWithMetadata(step Step, pg bool, sourceColumns ...JSONTypeSourceColumn) Step {
	names, types := step.Columns, step.Types
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		JSONTypeMetadataCheck(t, rows, pg, names, types, sourceColumns...)
	}
	return step
}
