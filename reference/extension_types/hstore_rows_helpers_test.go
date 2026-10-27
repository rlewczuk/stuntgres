package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const hstoreRowsInput = `a=>x,b=>NULL,c=>"NULL",""=>"",d=>x`
const hstoreRowsEscaped = `"é\\\""=>"雪\\\""`

func hstoreRowsPairs() [][]any {
	return [][]any{{"a", "x"}, {"b", nil}, {"c", "NULL"}, {"", ""}, {"d", "x"}}
}

func hstoreRowsValues(name string) [][]any {
	switch name {
	case "skeys":
		return [][]any{{"a"}, {"b"}, {"c"}, {""}, {"d"}}
	case "svals":
		return [][]any{{"x"}, {nil}, {"NULL"}, {""}, {"x"}}
	default:
		return hstoreRowsPairs()
	}
}

func hstoreRowsNames(name string) []string {
	if name == "each" {
		return []string{"key", "value"}
	}
	return []string{name}
}

// Bag comparisons deliberately retain duplicate rows and distinguish nil from
// text "NULL". Do not index expected rows from CheckScan: traversal is unordered.
func hstoreRowsSelect(q string, names []string, want [][]any, args ...any) fixture.Step {
	types := make([]string, len(names))
	for i := range types {
		types[i] = "TEXT"
	}
	s := fixture.Select(q, names, types, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		hstoreRowsTextMetadata(t, columns, names)
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		cells := make([]any, len(names))
		text := make([]sql.NullString, len(names))
		raw := make([]any, len(names))
		for i := range cells {
			cells[i] = &raw[i]
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		for i := range cells {
			cells[i] = &text[i]
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		for i := range text {
			if text[i].Valid != (raw[i] != nil) || (text[i].Valid && text[i].String != fixture.Normalize(raw[i])) {
				t.Fatalf("NullString column %d: %#v versus %#v", i, text[i], raw[i])
			}
		}
	}
	return s
}

func hstoreRowsTextMetadata(t *testing.T, columns []*sql.ColumnType, names []string) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("columns %d want %d", len(columns), len(names))
	}
	for i, c := range columns {
		if c.Name() != names[i] || c.DatabaseTypeName() != "TEXT" || c.ScanType() != reflect.TypeFor[string]() {
			t.Fatalf("column %d: %q/%q/%v want %q/TEXT/string", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i])
		}
		if n, ok := c.Nullable(); n || ok {
			t.Fatalf("computed %s nullability %v/%v", c.Name(), n, ok)
		}
		if n, ok := c.Length(); n != math.MaxInt64 || !ok {
			t.Fatalf("text length %d/%v want MaxInt64/true", n, ok)
		}
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("text precision %d/%d/%v", p, s, ok)
		}
	}
}

func hstoreRowsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	hstoreRun(t, p, b, db, s)
	hstoreRun(t, p, b, db, fixture.Prepared(s))
}

func hstoreRowsSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_rows(id integer,v hstore)", 0))
	for i, v := range []any{hstoreRowsInput, "", nil, "a=>NULL", hstoreRowsEscaped} {
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_rows VALUES ($1,$2::hstore)", 1, int64(i+1), v))
	}
}
