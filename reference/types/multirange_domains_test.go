package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMultirangeDomains(t *testing.T) {
	for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
		t.Run(typ, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, q := range []string{
					"CREATE DOMAIN public.span AS pg_catalog." + typ,
					"CREATE DOMAIN public.chain AS public.span",
					"CREATE TABLE r(k integer NOT NULL,a public.chain)",
					"CREATE TABLE guard(k integer NOT NULL,a public.chain NOT NULL)",
				} {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'{[1,3),[2,4),empty,[8,9)}'),(2,'{}'),(3,NULL)", 3))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 4, []byte(nil))))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 5, []byte("{(,)}"))))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO guard VALUES (1,'{[7,8)}')", 1))
				check := rangeSelect(backend, "SELECT k,a FROM r ORDER BY k", []string{"k", "a"}, []string{"INT4", strings.ToUpper(typ)}, [][]any{{int64(1), "{[1,4),[8,9)}"}, {int64(2), "{}"}, {int64(3), nil}, {int64(4), nil}, {int64(5), "{(,)}"}}, []bool{false, true})
				guard := rangeSelect(backend, "SELECT k,a FROM guard ORDER BY k", check.Columns, []string{"INT4", strings.ToUpper(typ)}, [][]any{{int64(1), "{[7,8)}"}}, []bool{false, false})
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "guard" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "guard", Column: "a"}
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{"INSERT INTO guard VALUES (2,$1)", "INSERT INTO guard VALUES (0,'{[1,2)}'),(2,$1)"} {
						s := fixture.ErrorDiagnostic(q, want, nil)
						s.Prepared, s.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, guard)
					}
					check.Prepared, guard.Prepared = prepared, prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, guard)
				}
				// Existing native domain-constraint boundary is distinct from
				// supported NOT NULL on a column over this same concrete base.
				q := "CREATE DOMAIN constrained AS " + typ + " NOT NULL"
				if backend.PG {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
				} else {
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "domain constraints and defaults are unsupported", Position: strconv.Itoa(strings.Index(q, "NOT NULL") + 1)}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.ErrorDiagnostic(q, want))
				}
			}
		})
	}
}
