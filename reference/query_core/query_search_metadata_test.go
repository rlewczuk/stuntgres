package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchMetadata(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, mode := range searchModes {
			for _, shape := range []string{"populated", "empty anchor", "limit zero"} {
				t.Run(mode+" "+shape, func(t *testing.T) {
					anchor := "SELECT 1"
					if shape == "empty anchor" {
						anchor += " WHERE FALSE"
					}
					q := "WITH RECURSIVE r(n) AS (" + anchor + " UNION ALL SELECT n FROM r WHERE FALSE)" + searchClause(mode, "n") + "SELECT * FROM r"
					if shape == "limit zero" {
						q += " LIMIT 0"
					}
					seq := "{(1)}"
					if mode == "BREADTH" {
						seq = "(0,1)"
					}
					var want [][]any
					if shape == "populated" {
						want = [][]any{{int64(1), seq}}
					}
					s := fixture.Select(q, []string{"n", "seq"}, []string{"INT4", searchType(mode)}, want)
					s.CheckMetadata = searchMetadata(b, s.Columns, s.Types)
					s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
						if column != 1 {
							return false
						}
						searchRawSequence(t, b, value, seq)
						return true
					}
					recursiveCTERoutes(t, p, b, c, s)
				})
			}
		}
	})
}

// These adapter contracts are asserted before Next, including empty results.
func searchMetadata(b fixture.Backend, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(names) {
			t.Fatal(columns, err)
		}
		for i, col := range columns {
			if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
				t.Errorf("column %d name/type=%s/%s want=%s/%s", i, col.Name(), col.DatabaseTypeName(), names[i], types[i])
			}
			scan := reflect.TypeFor[string]()
			switch types[i] {
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "RECORD", "_RECORD":
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			}
			if col.ScanType() != scan {
				t.Errorf("column %d ScanType=%v want=%v", i, col.ScanType(), scan)
			}
			if n, known := col.Nullable(); n || known {
				t.Errorf("column %d Nullable=%t/%t want=false/false", i, n, known)
			}
			var length int64
			lengthKnown := types[i] == "TEXT"
			if lengthKnown {
				length = 1<<63 - 1
			}
			if n, known := col.Length(); n != length || known != lengthKnown {
				t.Errorf("column %d Length=%d/%t want=%d/%t", i, n, known, length, lengthKnown)
			}
			if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Errorf("column %d DecimalSize=%d,%d/%t want=0,0/false", i, precision, scale, known)
			}
		}
	}
}

func searchRawSequence(t *testing.T, b fixture.Backend, value any, want string) {
	t.Helper()
	if b.PG {
		if _, ok := value.([]byte); !ok {
			t.Errorf("raw sequence type=%T want=[]byte", value)
		}
	} else {
		if _, ok := value.(string); !ok {
			t.Errorf("raw sequence type=%T want=string", value)
		}
	}
	if fixture.Normalize(value) != want {
		t.Errorf("raw sequence=%#v want=%q", value, want)
	}
}
