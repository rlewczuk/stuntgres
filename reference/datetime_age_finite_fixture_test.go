package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// Expected contracts are independently derived from the manual and REL_18_0.
// Main's pinned PostgreSQL 18.0 discovery confirmed the semantic, diagnostic,
// metadata and resolution cases; native cancellation is a separate API contract.
func datetimeAgeFiniteScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func datetimeAgeFiniteBoth(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.prepared = prepared
			var retained []*sql.ColumnType
			if s.selectRows && s.state == "" {
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
					var err error
					retained, err = rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					datetimeArithmeticMetadata(t, b.pg, retained)
				}
			}
			if s.state != "" {
				s.errorStage = "execute"
				if prepared && s.diagnostic.Position != "" {
					s.errorStage = "prepare"
				}
			}
			referenceRunStep(t, p.ctx, x, b.recorder, s)
			if retained != nil {
				datetimeArithmeticMetadata(t, b.pg, retained)
			}
		})
	}
}

func datetimeAgeFiniteValue(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, expression, typ string, value any) {
	t.Helper()
	datetimeAgeFiniteBoth(t, p, b, x, referenceSelect("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{value}}))
}

func datetimeAgeFiniteFailure(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q, code, message, hint string, position int, args ...any) {
	t.Helper()
	d := controlsReferenceDiagnostic(code, message)
	d.Hint, d.Position = hint, referencePosition(position)
	s := referenceErrorDiagnostic(q, d, args...)
	s.selectRows = true
	datetimeAgeFiniteBoth(t, p, b, x, s)
}
