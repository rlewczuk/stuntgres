package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func lqueryScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func lqueryInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION ltree", 0))
}

func lquerySelect(b fixture.Backend, q string, columns []string, values [][]any, args ...any) fixture.Step {
	types := make([]string, len(columns))
	for i, name := range columns {
		if name == "id" { types[i] = "INT4" } else if !b.PG { types[i] = "LQUERY" }
	}
	s := fixture.Select(q, columns, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if columns[column] == "id" { return false }
		if value == nil { return true }
		if b.PG {
			if _, ok := value.([]byte); !ok { t.Fatalf("pq lquery raw value %T", value) }
		} else if _, ok := value.(string); !ok { t.Fatalf("native lquery raw value %T", value) }
		return true
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		raw := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range raw { dest[i] = &raw[i] }
		if err := rows.Scan(dest...); err != nil { t.Fatal(err) }
		for i, value := range raw {
			if columns[i] == "id" || value == nil { continue }
			if b.PG {
				if _, ok := value.([]byte); !ok { t.Fatalf("pq lquery raw value %T", value) }
			} else if _, ok := value.(string); !ok { t.Fatalf("native lquery raw value %T", value) }
		}
	}
	return s
}

func lqueryMetadata(t *testing.T, pg bool, rows *sql.Rows, names []string, nullable []bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil { t.Fatal(err) }
	lqueryColumnMetadata(t, pg, columns, names, nullable)
	return columns
}

func lqueryColumnMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names []string, nullable []bool) {
	t.Helper()
	if len(columns) != len(names) { t.Fatalf("columns %d want %d", len(columns), len(names)) }
	for i, c := range columns {
		if c.Name() != names[i] { t.Fatalf("column name %q want %q", c.Name(), names[i]) }
		if names[i] == "id" { continue }
		name, scan := "LQUERY", reflect.TypeFor[string]()
		if pg { name, scan = "", reflect.TypeFor[any]() }
		if c.DatabaseTypeName() != name || c.ScanType() != scan { t.Fatalf("type %q/%v want %q/%v", c.DatabaseTypeName(), c.ScanType(), name, scan) }
		if n, known := c.Nullable(); pg || nullable == nil {
			if n || known { t.Fatalf("computed/pq nullability %v/%v", n, known) }
		} else if !known || n != nullable[i] { t.Fatalf("nullable %v/%v want %v/true", n, known, nullable[i]) }
		if n, known := c.Length(); n != 0 || known { t.Fatalf("length %d/%v", n, known) }
		if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known { t.Fatalf("decimal %d/%d/%v", p, s, known) }
	}
}

func lqueryError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, prepared, selectRows, prepareFailure bool, want fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, selectRows, "execute"
	if prepared && prepareFailure { s.ErrorStage = "prepare" }
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

var lqueryPositiveFixtures = []struct{ input, output string }{
	{"Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain", "Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain"},
	{"foo*@%@@**", "foo%@*"}, {"!Foo@|Bar%*{02,004}", "!Foo@|Bar%*{2,4}"},
	{"foo", "foo"}, {"foo{1}", "foo{1}"}, {"foo{1,1}", "foo{1}"},
	{"foo{0,65535}", "foo{,}"}, {"foo{2,65535}", "foo{2,}"},
	{"*", "*"}, {"*{,}", "*"}, {"*{0,65535}", "*"}, {"*{0}", "*{0}"}, {"*{65535,}", "*{65535}"},
	{"a{0,2}", "a{,2}"}, {"a{,2}", "a{,2}"}, {"a{,}", "a{,}"}, {"a{2,}", "a{2,}"},
	{"42._.-.a-b_c", "42._.-.a-b_c"}, {"b|a|b", "b|a|b"},
	{"4|3|2", "4|3|2"}, {"*.1.4|3|2.*", "*.1.4|3|2.*"}, {"1.*.4|3|2.*{,4}", "1.*.4|3|2.*{,4}"},
}
