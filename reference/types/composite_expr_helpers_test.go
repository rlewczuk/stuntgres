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

func compositeExprSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (i integer,t text)`, `CREATE TYPE outer_pair AS (p pair,n integer)`, `CREATE TABLE holder(k integer,v pair,w outer_pair)`)
}

// Every expected cell is specified before either backend executes. RECORD has a
// known SQL identity but pq transports it as bytes, unlike the native string.
func compositeExprSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if s.Types[col] != "RECORD" && s.Types[col] != "" {
			return false
		}
		want := s.Rows[row][col]
		if want == nil {
			if v != nil {
				t.Fatalf("raw NULL got %T %#v", v, v)
			}
			return true
		}
		if b.PG {
			x, ok := v.([]byte)
			if !ok || x == nil || string(x) != want {
				t.Fatalf("raw record got %T %#v want []byte(%q)", v, v, want)
			}
		} else {
			x, ok := v.(string)
			if !ok || x != want {
				t.Fatalf("raw record got %T %#v want string %q", v, v, want)
			}
		}
		return true
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func compositeExprBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(q, names, types, want, args...)
		s.Prepared = prepared
		compositeExprSelect(t, p, b, c, s)
	}
}

func compositeExprPosition(q, base string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, base)]) + 1)
}

func compositeExprError(t *testing.T, p *fixture.Pair, c *sql.Conn, q, code, message, base string) {
	t.Helper()
	d := fixture.CompositeReferenceDiagnostic(code, message)
	if base != "" {
		d.Position = compositeExprPosition(q, base)
	}
	fixture.CompositeReferenceError(t, p, c, q, "prepare", d)
}

func compositeExprRecordMetadata(pg bool, names []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, r *sql.Rows) {
		ts, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(ts) != len(names) {
			t.Fatalf("metadata width %d want %d", len(ts), len(names))
		}
		for i, typ := range ts {
			scan := reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
			l, lk := typ.Length()
			p, s, pk := typ.DecimalSize()
			n, nk := typ.Nullable()
			if typ.Name() != names[i] || typ.DatabaseTypeName() != "RECORD" || typ.ScanType() != scan || l != 0 || lk || p != 0 || s != 0 || pk || n || nk {
				t.Errorf("anonymous metadata[%d] name=%q type=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t; want %q RECORD %v unknown flags", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), l, lk, p, s, pk, n, nk, names[i], scan)
			}
		}
	}
}
