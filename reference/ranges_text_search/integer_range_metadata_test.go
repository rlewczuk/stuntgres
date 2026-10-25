package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntegerRangeMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(k integer,a int4range NOT NULL,b pg_catalog.int8range)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE n(a int4range,b int8range)", 0))
		for _, prepared := range []bool{false, true} {
			s := rangeSelect(backend, "SELECT a,b FROM r", []string{"a", "b"}, []string{"INT4RANGE", "INT8RANGE"}, nil, []bool{false, true})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO n VALUES (NULL,NULL)", 1))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'empty','[,]'),(2,'[1,4]',NULL)", 2))
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				rangeSelect(backend, "SELECT a,b FROM n", []string{"a", "b"}, []string{"INT4RANGE", "INT8RANGE"}, [][]any{{nil, nil}}, []bool{true, true}),
				rangeSelect(backend, "SELECT a,b FROM r ORDER BY k", []string{"a", "b"}, []string{"INT4RANGE", "INT8RANGE"}, [][]any{{"empty", "(,)"}, {"[1,5)", nil}}, []bool{false, true}),
				rangeSelect(backend, "SELECT a,b FROM r WHERE k=0", []string{"a", "b"}, []string{"INT4RANGE", "INT8RANGE"}, nil, []bool{false, true}),
			} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		}
	}
}
