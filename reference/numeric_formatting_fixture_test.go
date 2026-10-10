package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Expectations originate in PG18 documentation/release regression output and
// source reasoning recorded in the living plan, never native execution.
type numericFormattingCase struct {
	name, expression, typ string
	want                  any
}

func numericFormattingQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func numericFormattingScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func numericFormattingBoth(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, s referenceStep) {
	t.Helper()
	if s.diagnostic != nil && s.errorStage == "" {
		s.errorStage = "execute"
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.prepared = prepared
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		})
	}
}

func numericFormattingCases(t *testing.T, cases []numericFormattingCase) {
	t.Helper()
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, v := range cases {
			t.Run(v.name, func(t *testing.T) {
				typ := v.typ
				if typ == "" {
					typ = "TEXT"
				}
				numericFormattingSelect(t, p, b, c, "SELECT "+v.expression+" AS result", []string{"result"}, []string{typ}, [][]any{{v.want}})
			})
		}
	})
}

// Every value witness asserts concrete metadata and raw adapter representation
// before semantic values. The expectation is never derived from either backend.
func numericFormattingSelect(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, query string, names, types []string, values [][]any, args ...any) {
	t.Helper()
	s := referenceSelect(query, names, types, values, args...)
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) { numericFormattingMetadata(t, rows, b.pg) }
	s.checkRaw = func(t *testing.T, i, j int, v any) bool { referenceAssertRawValue(t, b.pg, types[i], v); return true }
	numericFormattingBoth(t, p, b, c, s)
}

func numericFormattingError(query string, d referenceDiagnostic, args ...any) referenceStep {
	s := referenceErrorDiagnostic(query, d, args...)
	s.selectRows, s.errorStage = true, "execute"
	return s
}

func numericFormattingAnalysisError(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, query string, d referenceDiagnostic, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s := numericFormattingError(query, d, args...)
			s.prepared = prepared
			if prepared {
				s.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		})
	}
}
