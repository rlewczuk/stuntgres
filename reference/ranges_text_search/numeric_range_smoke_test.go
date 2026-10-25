package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericRangeFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,v numrange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'(1.25,4.7500]')", 1))
		for i, value := range []any{"[1.0,1.00]", "[1.0,1.00)", "[9007199254740992.0000000000000001,9007199254740992.0000000000000002]", "[Infinity,Infinity]", "[,]", nil, []byte(nil)} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, i+2, value)))
		}
		data := []byte("[001.2300,2e1]")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 9, data)))
		for i := range data { data[i] = 'x' }
		want := [][]any{{int64(1), "(1.25,4.7500]"}, {int64(2), "[1.0,1.00]"}, {int64(3), "empty"}, {int64(4), "[9007199254740992.0000000000000001,9007199254740992.0000000000000002]"}, {int64(5), "[Infinity,Infinity]"}, {int64(6), "(,)"}, {int64(7), nil}, {int64(8), nil}, {int64(9), "[1.2300,20]"}}
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "NUMRANGE"}, want, []bool{false, true}),
			rangeSelect(b, "SELECT k,v FROM r WHERE k=99", []string{"k", "v"}, []string{"INT4", "NUMRANGE"}, nil, []bool{false, true}),
		}
		for _, tc := range []integerRangeFailure{
			{"[oops,4)", "22P02", `invalid input syntax for type numeric: "oops"`, ""},
			{"[0,1e-16384)", "22003", "value overflows numeric format", ""},
			{"[4.01,4.00)", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		} {
			diagnostic := numericRangeDiagnostic(tc)
			diagnostic.Context = `unnamed portal parameter $1 = '...'`
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO r VALUES (90,'[0,1)'),(91,$1)", diagnostic, tc.input)))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, checks[0])
		}
		for _, s := range checks { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s)) }
		numericRangeControlCycle(t, p, b, checks, nil)
	}
}
