package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func viewCheckRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
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

func viewCheckError(query, view, values string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "44000", Message: "new row violates check option for view \"" + view + "\"", Detail: "Failing row contains (" + values + ")."}, args...)
	s.ErrorStage = "execute"
	return s
}

func viewCheckRun(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool, steps ...fixture.Step) {
	t.Helper()
	for _, s := range steps {
		dml := strings.HasPrefix(s.Query, "INSERT") || strings.HasPrefix(s.Query, "UPDATE") || strings.HasPrefix(s.Query, "DELETE")
		if dml {
			viewWriteRun(t, p, b, held, s)
			continue
		}
		s.Prepared = held
		if s.State != "" && s.ErrorStage == "" {
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func viewCheckPairRows(rows ...[]any) fixture.Step {
	return viewWriteBag("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, rows)
}
