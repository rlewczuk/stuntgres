package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			for _, mark := range []struct {
				name, clause, typ string
				value             any
			}{
				{"boolean", "", "BOOL", false},
				{"text", " TO 'Y' DEFAULT 'N'", "TEXT", "N"},
				{"integer", " TO 1 DEFAULT 0", "INT4", int64(0)},
			} {
				for _, shape := range []string{"populated", "empty anchor", "limit zero"} {
					t.Run(mark.name+" "+shape, func(t *testing.T) {
						anchor := "SELECT 1"
						if shape == "empty anchor" {
							anchor += " WHERE FALSE"
						}
						q := "WITH RECURSIVE r(n) AS (" + anchor + " UNION ALL SELECT n FROM r WHERE FALSE) CYCLE n SET cyc" + mark.clause + " USING path SELECT * FROM r"
						if shape == "limit zero" {
							q += " LIMIT 0"
						}
						var want [][]any
						if shape == "populated" {
							want = [][]any{{int64(1), mark.value, "{(1)}"}}
						}
						t.Run("independent rows all routes", func(t *testing.T) {
							s := cteBag(q, []string{"n", "cyc", "path"}, []string{"INT4", mark.typ, "_RECORD"}, want)
							s.CheckMetadata = cycleMetadata(backend, s.Columns, s.Types)
							recursiveCTERoutes(t, p, backend, c, s)
						})
					})
				}
			}
		})
	}
}

func cycleMetadata(b fixture.Backend, names, types []string) func(*testing.T, *sql.Rows) {
	return cycleMarkMetadata(b, names, types, 0, 0, 0)
}

// Mark modifiers are common output Usage, not modifiers of either input alone.
func cycleMarkMetadata(b fixture.Backend, names, types []string, markLength, markPrecision, markScale int64) func(*testing.T, *sql.Rows) {
	return cycleColumnMetadata(b, names, types, markLength, markPrecision, markScale, false, false)
}

// Stored native view columns retain the existing known-nullable contract;
// pq does not expose nullability. All other descriptors stay identical.
func cycleStoredMarkMetadata(b fixture.Backend, names, types []string) func(*testing.T, *sql.Rows) {
	return cycleColumnMetadata(b, names, types, 0, 0, 0, !b.PG, !b.PG)
}

func cycleColumnMetadata(b fixture.Backend, names, types []string, markLength, markPrecision, markScale int64, nullable, nullableKnown bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(names) {
			t.Fatal(columns, err)
		}
		for i, c := range columns {
			if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
				t.Errorf("column %d name/type=%s/%s want=%s/%s", i, c.Name(), c.DatabaseTypeName(), names[i], types[i])
			}
			want := reflect.TypeFor[string]()
			switch types[i] {
			case "INT4":
				want = reflect.TypeFor[int64]()
				if b.PG {
					want = reflect.TypeFor[int32]()
				}
			case "INT8":
				want = reflect.TypeFor[int64]()
			case "BOOL":
				want = reflect.TypeFor[bool]()
			}
			if b.PG && (types[i] == "_RECORD" || types[i] == "NUMERIC" || types[i] == "BPCHAR") {
				want = reflect.TypeFor[any]()
			}
			if c.ScanType() != want {
				t.Errorf("column %d scan=%v want=%v", i, c.ScanType(), want)
			}
			if value, known := c.Nullable(); value != nullable || known != nullableKnown {
				t.Errorf("column %d nullable=%t/%t want=%t/%t", i, value, known, nullable, nullableKnown)
			}
			var length int64
			// Ordinary unbounded TEXT has this Length contract on both adapters,
			// independently of CYCLE or whether rows exist.
			lengthKnown := types[i] == "TEXT"
			if lengthKnown {
				length = 1<<63 - 1
			}
			if i == 1 && markLength > 0 {
				length = markLength
				lengthKnown = true
			}
			if value, known := c.Length(); value != length || known != lengthKnown {
				t.Errorf("column %d length=%d/%t want=%d/%t", i, value, known, length, lengthKnown)
			}
			var precision, scale int64
			decimalKnown := false
			if types[i] == "NUMERIC" {
				if i == 1 && markPrecision > 0 {
					precision = markPrecision
					scale = markScale
					decimalKnown = true
				} else if b.PG {
					precision = 65535
					scale = 65531
					decimalKnown = true
				}
			}
			if p, s, known := c.DecimalSize(); p != precision || s != scale || known != decimalKnown {
				t.Errorf("column %d decimal=%d,%d/%t want=%d,%d/%t", i, p, s, known, precision, scale, decimalKnown)
			}
		}
	}
}
