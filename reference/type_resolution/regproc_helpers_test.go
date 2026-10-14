package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func regprocScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c, b.Recorder)
		})
	}
}

func regprocStep(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
	t.Helper()
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, col := range cols {
			if s.Types[i] != "REGPROC" && s.Types[i] != "REGPROCEDURE" && s.Types[i] != "_REGPROC" && s.Types[i] != "_REGPROCEDURE" {
				continue
			}
			wantScan := reflect.TypeFor[string]()
			if r.RawBackend == fixture.RawPostgres {
				wantScan = reflect.TypeFor[any]()
			}
			length, lengthOK := col.Length()
			precision, scale, decimalOK := col.DecimalSize()
			nullable, nullableOK := col.Nullable()
			// The pinned PostgreSQL discovery run confirms pq's raw metadata.
			if col.Name() != s.Columns[i] || col.DatabaseTypeName() != s.Types[i] || col.ScanType() != wantScan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || r.RawBackend == fixture.RawPostgres && (nullable || nullableOK) {
				t.Errorf("metadata column=%d name=%q family=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t want=%s/%s/%v", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, s.Columns[i], s.Types[i], wantScan)
			}
		}
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		family := s.Types[column]
		if family != "REGPROC" && family != "REGPROCEDURE" && family != "_REGPROC" && family != "_REGPROCEDURE" {
			return false
		}
		if value == nil {
			return true
		}
		want := reflect.TypeFor[string]()
		if r.RawBackend == fixture.RawPostgres {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Errorf("raw column=%d row=%d family=%s got=%T/%#v want=%v", column, row, family, value, value, want)
		}
		return true
	}
	fixture.RunStep(t, p.Ctx, x, r, s)
}

func regprocBoth(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		regprocStep(t, p, x, r, s)
	}
}

// Independently specified full records, corrected against the pinned PostgreSQL
// observations in tests-1.md. Keep all optional fields in the comparison.
func regprocError(t *testing.T, err error, code, message, query, route, input string) {
	t.Helper()
	if err == nil {
		t.Errorf("route=%s query=%q unexpectedly succeeded", route, query)
		return
	}
	want := fixture.ControlsReferenceDiagnostic(code, message)
	// Type parser positions refer to the decoded argument except when an
	// outer literal replaces the position with its SQL source location.
	switch input {
	case "abs(integer(4))":
		want.Position = "8"
		want.Context = `invalid type name "integer(4)"`
	case "abs(NONE)":
		want.Position = "1"
		want.Context = `invalid type name "NONE"`
	}
	if strings.HasPrefix(route, "literal") || route == "held-literal" {
		want.Position = strconv.Itoa(utf8.RuneCountInString(query[:strings.Index(query, "'")]) + 1)
	} else if route == "direct-frame" || route == "held-frame" {
		if want.Context != "" {
			want.Context += "\n"
		}
		want.Context += "unnamed portal parameter $1 = '...'"
	}
	switch route {
	case "cast":
		if code == "42846" {
			want.Position = "12"
		}
	case "assignment":
		want.Position = "41"
		want.Hint = "You will need to rewrite or cast the expression."
	case "stored-dependency":
		want.Detail = "default value for column p of table defaults_subject depends on function unaccent(text)\nview constant_view depends on function unaccent(text)"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	case "atomic-insert":
		want.Position = "61"
	}
	got := fixture.ExtractDiagnostic(t, err)
	if got != want {
		t.Errorf("route=%s query=%q diagnostic got=%#v want=%#v", route, query, got, want)
	}
}
