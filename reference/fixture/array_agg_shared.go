package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func ArrayAggMetadata(pg bool, families []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != len(families) {
			t.Fatalf("metadata columns=%d want %d", len(cols), len(families))
		}
		for i, c := range cols {
			want := reflect.TypeFor[string]()
			if pg {
				want = reflect.TypeFor[any]()
			}
			if c.ScanType() != want {
				t.Fatalf("%s ScanType=%v want %v", c.Name(), c.ScanType(), want)
			}
			if n, known := c.Nullable(); n != !pg || known != !pg {
				t.Fatalf("nullable=%t/%t", n, known)
			}
			if n, known := c.Length(); n != 0 || known {
				t.Fatalf("length=%d/%t", n, known)
			}
			p, s, known := c.DecimalSize()
			if pg && families[i] == "_NUMERIC" {
				if p != 65535 || s != 65531 || !known {
					t.Fatalf("numeric precision=%d/%d/%t", p, s, known)
				}
			} else if p != 0 || s != 0 || known {
				t.Fatalf("precision=%d/%d/%t", p, s, known)
			}
		}
	}
}

func ArrayAggBoth(run func(Step), s Step) {
	run(s)
	run(Prepared(s))
}
