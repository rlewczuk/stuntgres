package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayTransformScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("CREATE TABLE at_one(id INT)", 0))
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("INSERT INTO at_one VALUES(1)", 1))
			body(t, p, backend, c)
		})
	}
}

func arrayTransformBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			copy := s
			copy.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, copy)
		})
	}
}

func arrayTransformSelect(b fixture.Backend, q string, names, types []string, rows [][]any, args ...any) fixture.Step {
	return fixture.ArrayReferenceSelect(q, names, types, types, rows, b.PG, args...)
}

type arrayTransformValue struct {
	name  string
	expr  string
	typ   string
	value any
}

func arrayTransformValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, cases []arrayTransformValue) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT "+tc.expr+" AS a FROM at_one", []string{"a"}, []string{tc.typ}, [][]any{{tc.value}}))
		})
	}
}

func arrayTransformDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			s := fixture.ImplicitError(q, d, args...)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared && analysis {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

type arrayTransformColumn struct {
	scan        string
	length      int64
	lengthKnown bool
}

type arrayTransformContract struct {
	names, nativeTypes, pgTypes, raw []string
	rows                             [][]any
	pgMetadata                       []arrayTransformColumn
	diagnostic                       fixture.Diagnostic
	analysis                         bool
}

func arrayTransformAssertContract(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, query string, args ...any) {
	t.Helper()
	key := query
	if strings.HasPrefix(name, "SET ") || strings.HasSuffix(name, "_all_accessors") {
		key = name
	}
	want, ok := arrayTransformContracts[key]
	if !ok {
		t.Fatalf("missing independent array transform expectation for %q", key)
	}
	arrayTransformRunContract(t, p, b, c, name, query, want, args...)
}

func arrayTransformTypingContract(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, expr string) {
	t.Helper()
	oracle := "SELECT " + expr + " AS a,pg_typeof(" + expr + ")::text AS type_name FROM at_one"
	want, ok := arrayTransformContracts[oracle]
	if !ok {
		t.Fatalf("missing independent typing expectation for %q", oracle)
	}
	arrayTransformRunContract(t, p, b, c, name, oracle, want)
}

func arrayTransformRunContract(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, query string, want arrayTransformContract, args ...any) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		if want.diagnostic.Code != "" {
			arrayTransformDiagnostic(t, p, b, c, query, want.diagnostic, want.analysis, args...)
			return
		}
		types := want.nativeTypes
		if b.PG {
			types = want.pgTypes
		}
		s := fixture.ArrayReferenceSelect(query, want.names, types, want.raw, want.rows, b.PG, args...)
		basic := s.CheckMetadata
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			basic(t, rows)
			cols, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, col := range cols {
				if n, known := col.Nullable(); n || known {
					t.Fatalf("%s Nullable=%t/%t", col.Name(), n, known)
				}
				if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
					t.Fatalf("%s DecimalSize=%d/%d/%t", col.Name(), precision, scale, known)
				}
				if b.PG {
					m := want.pgMetadata[i]
					scans := map[string]reflect.Type{"interface {}": reflect.TypeFor[any](), "string": reflect.TypeFor[string](), "[]uint8": reflect.TypeFor[[]byte]()}
					if col.ScanType() != scans[m.scan] {
						t.Fatalf("%s ScanType=%v want %s", col.Name(), col.ScanType(), m.scan)
					}
					if n, known := col.Length(); n != m.length || known != m.lengthKnown {
						t.Fatalf("%s Length=%d/%t want %d/%t", col.Name(), n, known, m.length, m.lengthKnown)
					}
				}
			}
		}
		arrayTransformBoth(t, p, b, c, s)
	})
}
