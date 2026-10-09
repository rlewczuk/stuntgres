package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

// Nullable and Length knowledge differ in pq, independently of row codecs.
func referenceInsertShortMetadata(t *testing.T, rows *sql.Rows, pg bool, names []string, nullable []bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != len(names) {
		t.Fatalf("descriptor count=%d want=%d", len(types), len(names))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		switch names[i] {
		case "INT4":
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "TEXT":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if typ.DatabaseTypeName() != names[i] || typ.ScanType() != scan {
			t.Fatalf("descriptor %d: %s %v", i, typ.DatabaseTypeName(), typ.ScanType())
		}
		n, known := typ.Nullable()
		if pg {
			if n || known {
				t.Fatalf("pq nullable=%t/%t", n, known)
			}
		} else if !known || n != nullable[i] {
			t.Fatalf("native nullable=%t/%t want=%t", n, known, nullable[i])
		}
		length, lengthKnown := typ.Length()
		wantLength, wantKnown := int64(0), false
		if names[i] == "TEXT" {
			wantLength, wantKnown = math.MaxInt64, true
		}
		if length != wantLength || lengthKnown != wantKnown {
			t.Fatalf("length=%d/%t want=%d/%t", length, lengthKnown, wantLength, wantKnown)
		}
	}
}
