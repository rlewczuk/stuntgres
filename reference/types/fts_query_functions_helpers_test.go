package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func ftsQueryFunctionsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func ftsQueryFunctionsValue(t *testing.T, p *fixture.Pair, b fixture.Backend, expression, typ string, want any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s := fixture.Select("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{want}}, args...)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		})
	}
}
