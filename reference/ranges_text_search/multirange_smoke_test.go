package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMultirangeFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(k integer,a int4multirange,b pg_catalog.int8multirange,c nummultirange)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'{[1,3),[2,4),empty,[8,9)}','{[1,3),[2,4),empty,[8,9)}','{[1.0,2.00),[2.000,3.0)}')", 1))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3,$4)", 1, 2, []byte("{}"), "{(,)}", "{(4,5)}")))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3,$4)", 1, 3, nil, []byte(nil), nil)))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (4,'{[1,2),[3,4)}','{[1,2),[3,4)}','{[1,2),(2,3]}')", 1))
		want := [][]any{
			{int64(1), "{[1,4),[8,9)}", "{[1,4),[8,9)}", "{[1.0,3.0)}"},
			{int64(2), "{}", "{(,)}", "{(4,5)}"}, {int64(3), nil, nil, nil},
			{int64(4), "{[1,2),[3,4)}", "{[1,2),[3,4)}", "{[1,2),(2,3]}"},
		}
		for _, prepared := range []bool{false, true} {
			s := rangeSelect(backend, "SELECT k,a,b,c FROM r ORDER BY k", []string{"k", "a", "b", "c"}, []string{"INT4", "INT4MULTIRANGE", "INT8MULTIRANGE", "NUMMULTIRANGE"}, want, []bool{true, true, true, true})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
	}
}
