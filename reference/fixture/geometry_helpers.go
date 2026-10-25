package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

// declared gives source nullability only where the query directly selects
// physical columns; expression nullability is deliberately not invented.
func BoxMetadata(t *testing.T, pg bool, rows *sql.Rows, types []string, declared []bool) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != len(types) {
		t.Fatalf("metadata width %d want %d", len(cols), len(types))
	}
	for i, c := range cols {
		if c.DatabaseTypeName() != types[i] {
			t.Fatalf("column %d type %s want %s", i, c.DatabaseTypeName(), types[i])
		}
		if types[i] == "BOX" || types[i] == "POINT" || types[i] == "LSEG" {
			want := reflect.TypeFor[string]()
			if pg {
				want = reflect.TypeFor[any]()
			}
			if c.ScanType() != want {
				t.Fatalf("column %d ScanType %v want %v", i, c.ScanType(), want)
			}
			if n, ok := c.Length(); n != 0 || ok {
				t.Fatalf("geometry length %d/%t", n, ok)
			}
			if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("geometry precision %d,%d/%t", p, s, ok)
			}
		}
		if declared != nil {
			n, ok := c.Nullable()
			known := !pg
			want := known && declared[i]
			if n != want || ok != known {
				t.Fatalf("column %d nullable %t/%t want %t/%t", i, n, ok, want, known)
			}
		}
	}
}

func PointConsumeRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	BoxMetadata(t, false, rows, []string{"INT4", "POINT"}, nil)
	var got [][]any
	for rows.Next() {
		var id int64
		var value any
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		if value != nil {
			if _, ok := value.(string); !ok {
				t.Fatalf("native POINT raw value %T", value)
			}
		}
		got = append(got, []any{id, value})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained POINT rows %#v want %#v", got, want)
	}
}
