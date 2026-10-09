package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func arrayReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TABLE public.one (v INT)", 0))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("INSERT INTO public.one VALUES (1)", 1))
			body(t, p, backend, conn)
		})
	}
}

func arrayReferenceQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func arrayReferenceInputError(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, input, typ string, want referenceDiagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		literal := want
		literal.Position = "8"
		s := referenceErrorDiagnostic("SELECT "+arrayReferenceQuote(input)+"::"+typ+" AS a FROM public.one", literal)
		s.selectRows = true
		s.prepared = prepared
		s.errorStage = "execute"
		if prepared {
			s.errorStage = "prepare"
		}
		referenceRunStep(t, p.ctx, c, b.recorder, s)
		parameter := want
		parameter.Context = "unnamed portal parameter $1 = '...'"
		if input == "" {
			parameter.Context = "unnamed portal parameter $1 = ''"
		}
		s = referenceErrorDiagnostic("SELECT $1::"+typ+" AS a FROM public.one", parameter, input)
		s.selectRows = true
		s.prepared = prepared
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
}

func arrayReferenceSelect(query string, names, types, raw []string, rows [][]any, pg bool, args ...any) referenceStep {
	s := referenceSelect(query, names, types, rows, args...)
	s.rawTypes = raw
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, family := range raw {
			if !strings.HasPrefix(family, "_") {
				continue
			}
			want := reflect.TypeFor[string]()
			if pg {
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
