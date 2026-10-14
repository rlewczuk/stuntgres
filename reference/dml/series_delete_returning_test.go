package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSeriesDeleteReturningRestrictions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DELETE FROM t RETURNING generate_series(1,2)", fixture.Diagnostic{Code: "0A000", Message: "set-returning functions are not allowed in RETURNING", Position: "25"})
				s.Prepared, s.SelectRows = prepared, true
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
		})
	}
}
