package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func legacySnapshotScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func legacySnapshotStep(b fixture.Backend, q string, names, kinds []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, kinds, values, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(kinds) {
			t.Fatalf("column descriptors %v: %v", columns, err)
		}
		for i, kind := range kinds {
			var scan reflect.Type
			switch kind {
			case "TXID_SNAPSHOT":
				scan = reflect.TypeFor[[]byte]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "TEXT":
				scan = reflect.TypeFor[string]()
			default:
				t.Fatalf("unexpected metadata kind %s", kind)
			}
			c := columns[i]
			if c.Name() != names[i] || c.DatabaseTypeName() != kind || c.ScanType() != scan {
				t.Fatalf("column %d = %s/%s/%v, want %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i], kind, scan)
			}
			if v, known := c.Nullable(); v || known {
				t.Fatalf("computed nullability %v/%v", v, known)
			}
			// Native Usage.Length and pq both expose unbounded TEXT with
			// the MaxInt64 length sentinel. TXID_SNAPSHOT has no length metadata.
			var wantLength int64
			wantLengthKnown := kind == "TEXT"
			if wantLengthKnown {
				wantLength = 1<<63 - 1
			}
			if v, known := c.Length(); v != wantLength || known != wantLengthKnown {
				t.Fatalf("column %d (%s) length %d/%v want %d/%v", i, kind, v, known, wantLength, wantLengthKnown)
			}
			if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known {
				t.Fatalf("computed decimal %d/%d/%v", p, s, known)
			}
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if kinds[col] != "TXID_SNAPSHOT" {
			return false
		}
		want := values[row][col]
		if want == nil {
			if v != nil {
				t.Fatalf("snapshot NULL got %T %v", v, v)
			}
		} else if raw, ok := v.([]byte); !ok || string(raw) != want {
			t.Fatalf("snapshot raw %T %v want bytes %v", v, v, want)
		}
		return true
	}
	return s
}

// Stored relation/view attributes have known native nullability, unlike direct
// computed expressions. These views declare no NOT NULL attribute.
func legacySnapshotStoredStep(b fixture.Backend, q string, name, kind string, values [][]any) fixture.Step {
	s := legacySnapshotStep(b, q, []string{name}, []string{kind}, values)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatalf("stored descriptors %v: %v", columns, err)
		}
		c := columns[0]
		wantNullable, wantKnown := true, true
		if b.PG {
			wantNullable, wantKnown = false, false
		}
		if v, known := c.Nullable(); v != wantNullable || known != wantKnown {
			t.Fatalf("stored %s nullability %v/%v want %v/%v", kind, v, known, wantNullable, wantKnown)
		}
	}
	return s
}
