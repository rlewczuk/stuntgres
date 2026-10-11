package fixture

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

func DriverBasicMetadata(t *testing.T, rows *sql.Rows, postgres bool, types []string, nullable []bool) {
	t.Helper()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != len(types) {
		t.Fatalf("descriptors %d want %d", len(meta), len(types))
	}
	for i, kind := range types {
		if meta[i].DatabaseTypeName() != kind {
			t.Fatalf("type %d %s want %s", i, meta[i].DatabaseTypeName(), kind)
		}
		var scan reflect.Type
		switch kind {
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if postgres {
				scan = reflect.TypeFor[int32]()
			}
		case "INT8":
			scan = reflect.TypeFor[int64]()
		case "TEXT":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		default:
			t.Fatalf("unspecified driver descriptor %s", kind)
		}
		if meta[i].ScanType() != scan {
			t.Fatalf("ScanType %d %v want %v", i, meta[i].ScanType(), scan)
		}
		value, known := meta[i].Nullable()
		if postgres {
			if value || known {
				t.Fatalf("pq Nullable %d %t/%t", i, value, known)
			}
		} else if !known || value != nullable[i] {
			t.Fatalf("native Nullable %d %t/%t want %t", i, value, known, nullable[i])
		}
		length, lengthKnown := meta[i].Length()
		if kind == "TEXT" {
			if length != math.MaxInt64 || !lengthKnown {
				t.Fatalf("TEXT Length %d/%t", length, lengthKnown)
			}
		} else if length != 0 || lengthKnown {
			t.Fatalf("Length %d %d/%t", i, length, lengthKnown)
		}
	}
}
