package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

// Stored-view outputs are relation columns, not computed SELECT expressions.
func JSONPathNavigateViewMetadataStep(s Step, pg bool) Step {
	s = JSONBSetMetadataStep(s, pg) // Retain the raw, rescan and byte-ownership checks.
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		column := columns[0]
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if column.Name() != "changed" || column.DatabaseTypeName() != "JSONB" || column.ScanType() != scan {
			t.Fatalf("view metadata=%q/%q/%v want changed/JSONB/%v", column.Name(), column.DatabaseTypeName(), column.ScanType(), scan)
		}
		if length, known := column.Length(); known {
			t.Fatalf("unexpected view length %d", length)
		}
		if precision, scale, known := column.DecimalSize(); known {
			t.Fatalf("unexpected view decimal %d,%d", precision, scale)
		}
		if nullable, known := column.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("view nullable=%v,%v want %v,%v", nullable, known, !pg, !pg)
		}
	}
	return s
}
