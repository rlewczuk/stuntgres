package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

// Expectations are independently specified from the manual and REL_18_0.
// Root's pinned PostgreSQL oracle validated the shared SQL definitions;
// native-only control phases remain separate public API contracts.
func datetimeJustifyScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle TO 'postgres'", 0))
			body(t, p, b, c)
		})
	}
}

func datetimeJustifyMetadata(t *testing.T, pg bool, rows *sql.Rows) {
	t.Helper()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range meta {
		if m.DatabaseTypeName() != "INTERVAL" {
			t.Fatalf("type = %q; want INTERVAL", m.DatabaseTypeName())
		}
		want := reflect.TypeFor[string]()
		if pg {
			want = reflect.TypeFor[any]()
		}
		if m.ScanType() != want {
			t.Fatalf("ScanType = %v; want %v", m.ScanType(), want)
		}
		if _, ok := m.Length(); ok {
			t.Fatal("interval has length metadata")
		}
		if _, _, ok := m.DecimalSize(); ok {
			t.Fatal("interval has decimal metadata")
		}
	}
}

func datetimeJustifyBoth(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.prepared = prepared
			if s.selectRows && s.state == "" {
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.pg, rows) }
			}
			if s.state != "" {
				s.errorStage = "execute"
				if prepared && s.diagnostic.Position != "" {
					s.errorStage = "prepare"
				}
			}
			referenceRunStep(t, p.ctx, x, b.recorder, s)
		})
	}
}

func datetimeJustifyValue(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, expr string, want any) {
	t.Helper()
	datetimeJustifyBoth(t, p, b, x, referenceSelect("SELECT "+expr+" AS v", []string{"v"}, []string{"INTERVAL"}, [][]any{{want}}))
}

func datetimeJustifyFailure(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q, code, message, hint string, position int) {
	t.Helper()
	d := controlsReferenceDiagnostic(code, message)
	d.Hint, d.Position = hint, referencePosition(position)
	s := referenceErrorDiagnostic(q, d)
	s.selectRows = true
	datetimeJustifyBoth(t, p, b, x, s)
}
