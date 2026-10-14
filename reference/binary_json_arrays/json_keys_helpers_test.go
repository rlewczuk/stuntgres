package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonKeysScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func jsonKeysSelect(q string, keys []string, args ...any) fixture.Step {
	var rows [][]any
	for _, key := range keys {
		rows = append(rows, []any{key})
	}
	s := fixture.Select(q, []string{"json_object_keys"}, []string{"TEXT"}, rows, args...)
	s.Bag = true
	return s
}

func jsonKeysBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

// Error expectations are complete pinned PostgreSQL 18.0 logical records.
func jsonKeysError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, prepared bool, want fixture.Diagnostic, stage string, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.SelectRows = true
	s.Prepared = prepared
	s.ErrorStage = stage
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}
