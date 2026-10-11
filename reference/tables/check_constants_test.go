package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckScalarConstants(t *testing.T) {
	for _, expr := range []string{"NULL", "TRUE", "'true'"} {
		t.Run(expr, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(a INT CHECK("+expr+"))", 0))
					fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(NULL),(1),(2)", 3))
				})
			}
		})
	}
}
