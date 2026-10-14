package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, held) })
			}
		})
	}
}

func ruleRun(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool, steps ...fixture.Step) {
	t.Helper()
	for _, s := range steps {
		s.Prepared = held
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func ruleRows(query string, columns, types []string, rows ...[]any) fixture.Step {
	return fixture.Select(query, columns, types, rows)
}

func ruleError(query, code, message string) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
}

func ruleInts(query string, rows ...[]any) fixture.Step {
	return ruleRows(query, []string{"id", "v"}, []string{"INT4", "INT4"}, rows...)
}
