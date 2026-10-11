package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericRangeMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,a numrange NOT NULL,b numrange)", 0))
		check := func(query string, rows [][]any) {
			s := rangeSelect(b, query, []string{"k", "a", "b"}, []string{"INT4", "NUMRANGE", "NUMRANGE"}, rows, []bool{false, false, true})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		check("SELECT k,a,b FROM r ORDER BY k", nil)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'empty',NULL)", 1))
		check("SELECT k,a,b FROM r ORDER BY k", [][]any{{int64(1), "empty", nil}})
		check("SELECT k,a,b FROM r WHERE k=99", nil)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (2,'(4,5)','[1.00,2.0]')", 1))
		check("SELECT k,a,b FROM r ORDER BY k", [][]any{{int64(1), "empty", nil}, {int64(2), "(4,5)", "[1.00,2.0]"}})
	}
}
