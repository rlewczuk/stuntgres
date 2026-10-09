package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

func driverBasicMetadata(t *testing.T, rows *sql.Rows, postgres bool, types []string, nullable []bool) {
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

func TestReferenceDriverRowsBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE metadata(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)", 0))
			for _, populated := range []bool{false, true} {
				if populated {
					run(referenceExec("INSERT INTO metadata VALUES(1,2,'x',FALSE)", 1))
				}
				var want [][]any
				if populated {
					want = [][]any{{int64(1), int64(2), "x", false}}
				}
				for _, prepared := range []bool{false, true} {
					s := referenceSelect("SELECT * FROM metadata", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, want)
					s.prepared = prepared
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						driverBasicMetadata(t, rows, backend.pg, s.types, []bool{false, true, true, true})
					}
					run(s)
				}
			}
		})
	}
}
