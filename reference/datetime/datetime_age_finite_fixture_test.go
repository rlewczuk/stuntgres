package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Expected contracts are independently derived from the manual and REL_18_0.
// Main's pinned PostgreSQL 18.0 discovery confirmed the semantic, diagnostic,
// metadata and resolution cases; native cancellation is a separate API contract.
func datetimeAgeFiniteScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func datetimeAgeFiniteBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.Prepared = prepared
			var retained []*sql.ColumnType
			if s.SelectRows && s.State == "" {
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					var err error
					retained, err = rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					datetimeArithmeticMetadata(t, b.PG, retained)
				}
			}
			if s.State != "" {
				s.ErrorStage = "execute"
				if prepared && s.Diagnostic.Position != "" {
					s.ErrorStage = "prepare"
				}
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			if retained != nil {
				datetimeArithmeticMetadata(t, b.PG, retained)
			}
		})
	}
}

func datetimeAgeFiniteValue(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, expression, typ string, value any) {
	t.Helper()
	datetimeAgeFiniteBoth(t, p, b, x, fixture.Select("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{value}}))
}

func datetimeAgeFiniteFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q, code, message, hint string, position int, args ...any) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Hint, d.Position = hint, fixture.Position(position)
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows = true
	datetimeAgeFiniteBoth(t, p, b, x, s)
}
