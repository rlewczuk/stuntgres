package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericRangeDomainControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, query := range []string{
			"CREATE DOMAIN public.span AS pg_catalog.numrange",
			"CREATE DOMAIN public.chain AS public.span",
			"CREATE TABLE r(k integer NOT NULL,a public.span,b public.chain)",
			"CREATE TABLE vacant(v public.chain NOT NULL)",
			"CREATE TABLE guard(k integer NOT NULL,v public.chain NOT NULL)",
		} { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(query, 0)) }
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'[1.0,1.00]','(4,5)'),(2,'empty','[,]'),(3,NULL,NULL)", 3))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 4, []byte("[-inf,+INF]"), "[Infinity,NaN]")))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 5, nil, []byte(nil))))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO guard VALUES (99,'[0,1)')", 1))
		want := [][]any{{int64(1), "[1.0,1.00]", "(4,5)"}, {int64(2), "empty", "(,)"}, {int64(3), nil, nil}, {int64(4), "[-Infinity,Infinity]", "[Infinity,NaN]"}, {int64(5), nil, nil}}
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "NUMRANGE", "NUMRANGE"}, want, []bool{false, true, true}),
			rangeSelect(b, "SELECT v FROM vacant", []string{"v"}, []string{"NUMRANGE"}, nil, []bool{false}),
			rangeSelect(b, "SELECT k,v FROM guard ORDER BY k", []string{"k", "v"}, []string{"INT4", "NUMRANGE"}, [][]any{{int64(99), "[0,1)"}}, []bool{false, false}),
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		diagnostic := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "guard" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "guard", Column: "v"}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO guard VALUES (1,'[7,8)'),(2,$1)", diagnostic, nil)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, checks[2])
		}
		script := "CREATE DOMAIN public.span AS numrange; CREATE DOMAIN public.chain AS public.span; CREATE TABLE guard(k integer NOT NULL,v public.chain NOT NULL); INSERT INTO guard VALUES (1,'[7,8)'); INSERT INTO guard VALUES (2,NULL);"
		numericRangeControlCycle(t, p, b, checks, []struct{ script string; diagnostic fixture.Diagnostic }{{script, diagnostic}})
	}
}
