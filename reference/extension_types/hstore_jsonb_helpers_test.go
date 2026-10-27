package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreJSONBQuery(input string) string {
	return "SELECT hstore_to_jsonb(" + input + ") AS strict_json,hstore_to_jsonb_loose(" + input + ") AS loose_json"
}

func hstoreJSONBSelect(b fixture.Backend, q string, names []string, values [][]any, stored bool, args ...any) fixture.Step {
	types := make([]string, len(names))
	for i, name := range names {
		types[i] = "JSONB"
		if name == "id" {
			types[i] = "INT4"
		}
	}
	s := fixture.Select(q, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if names[column] == "id" {
			return false
		}
		if v != nil {
			if b.PG {
				if _, ok := v.([]byte); !ok {
					t.Fatalf("raw JSON %T want []byte", v)
				}
			} else if _, ok := v.(string); !ok {
				t.Fatalf("raw JSON %T want string", v)
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range columns {
			if names[i] == "id" {
				continue
			}
			scan := reflect.TypeFor[string]()
			if b.PG {
				scan = reflect.TypeFor[any]()
			}
			if c.DatabaseTypeName() != "JSONB" || c.ScanType() != scan {
				t.Fatalf("JSON metadata %q/%v want JSON/%v", c.DatabaseTypeName(), c.ScanType(), scan)
			}
			n, known := c.Nullable()
			wantKnown := stored && !b.PG
			if n != wantKnown || known != wantKnown {
				t.Fatalf("JSON nullable %v/%v want %v/%v", n, known, wantKnown, wantKnown)
			}
			if n, ok := c.Length(); n != 0 || ok {
				t.Fatalf("JSON length %d/%v", n, ok)
			}
			if p, scale, ok := c.DecimalSize(); p != 0 || scale != 0 || ok {
				t.Fatalf("JSON decimal %d/%d/%v", p, scale, ok)
			}
		}
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
		cells := make([]sql.NullString, len(names))
		dest := make([]any, len(names))
		for i := range dest {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, name := range names {
			if name == "id" {
				continue
			}
			want := values[index][i]
			if cells[i].Valid != (want != nil) || (want != nil && cells[i].String != want) {
				t.Fatalf("JSON scan %#v want %#v", cells[i], want)
			}
		}
		if len(names) == 1 && values[index][0] != nil {
			var text string
			if err := rows.Scan(&text); err != nil || text != values[index][0] {
				t.Fatalf("string scan %q/%v", text, err)
			}
			var detached []byte
			if err := rows.Scan(&detached); err != nil || string(detached) != values[index][0] {
				t.Fatalf("bytes scan %q/%v", detached, err)
			}
			if len(detached) != 0 {
				detached[0] ^= 1
			}
			if err := rows.Scan(&text); err != nil || text != values[index][0] {
				t.Fatalf("detached bytes changed JSON %q/%v", text, err)
			}
		}
	}
	return s
}

func hstoreJSONBPair(b fixture.Backend, q string, values [][]any, args ...any) fixture.Step {
	return hstoreJSONBSelect(b, q, []string{"strict_json", "loose_json"}, values, false, args...)
}
