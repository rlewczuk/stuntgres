package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations originate in PG18 documentation/release regression output and
// source reasoning recorded in the living plan, never native execution.
type numericFormattingCase struct {
	name, expression, typ string
	want                  any
}

func numericFormattingQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func numericFormattingScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
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

func numericFormattingBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	if s.Diagnostic != nil && s.ErrorStage == "" {
		s.ErrorStage = "execute"
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func numericFormattingCases(t *testing.T, cases []numericFormattingCase) {
	t.Helper()
	numericFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
func numericFormattingSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, names, types []string, values [][]any, args ...any) {
	t.Helper()
	s := fixture.Select(query, names, types, values, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { numericFormattingMetadata(t, rows, b.PG) }
	s.CheckRaw = func(t *testing.T, i, j int, v any) bool { fixture.AssertRawValue(t, b.PG, types[i], v); return true }
	numericFormattingBoth(t, p, b, c, s)
}

func numericFormattingError(query string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(query, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func numericFormattingAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s := numericFormattingError(query, d, args...)
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}
