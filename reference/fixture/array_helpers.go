package fixture

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func ArrayReferenceScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			RunStep(t, p.Ctx, conn, backend.Recorder, Exec("CREATE TABLE public.one (v INT)", 0))
			RunStep(t, p.Ctx, conn, backend.Recorder, Exec("INSERT INTO public.one VALUES (1)", 1))
			body(t, p, backend, conn)
		})
	}
}

func ArrayReferenceQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func ArrayReferenceInputError(t *testing.T, p *Pair, b Backend, c *sql.Conn, input, typ string, want Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		literal := want
		literal.Position = "8"
		s := ErrorDiagnostic("SELECT "+ArrayReferenceQuote(input)+"::"+typ+" AS a FROM public.one", literal)
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		RunStep(t, p.Ctx, c, b.Recorder, s)
		parameter := want
		parameter.Context = "unnamed portal parameter $1 = '...'"
		if input == "" {
			parameter.Context = "unnamed portal parameter $1 = ''"
		}
		s = ErrorDiagnostic("SELECT $1::"+typ+" AS a FROM public.one", parameter, input)
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func ArrayReferenceSelect(query string, names, types, raw []string, rows [][]any, pg bool, args ...any) Step {
	s := Select(query, names, types, rows, args...)
	s.RawTypes = raw
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, family := range raw {
			if !strings.HasPrefix(family, "_") {
				continue
			}
			want := reflect.TypeFor[string]()
			// The catalog's NULL-only pseudo carrier has no concrete array
			// codec; native deliberately exposes interface{} just like pq.
			if pg || types[i] == "ANYARRAY" {
				want = reflect.TypeFor[any]()
			}
			if columns[i].ScanType() != want {
				t.Fatalf("column %s ScanType=%v want %v", names[i], columns[i].ScanType(), want)
			}
			if length, known := columns[i].Length(); length != 0 || known {
				t.Fatalf("array Length=%d/%t", length, known)
			}
		}
	}
	return s
}
