package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLXPathSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "xpath_schema", "xpath_schema_new")
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			xpathStep(b, xpathSingleton("/r/a[2]", "<r><a>x</a><a>y</a></r>", ""), "TEXT", "<a>y</a>"),
			xpathArrayStep("SELECT xpath('/missing','<r/>'::xml) AS v", b.PG, "{}"),
			xpathStep(b, "SELECT xpath_exists('/missing','<r/>'::xml) AS v", "BOOL", false),
			xpathStep(b, xpathSingleton("/q:r", `<r xmlns="urn:x"/>`, `'[0:0][-1:0]={{q,urn:x}}'::text[]`), "TEXT", `<r xmlns="urn:x"/>`),
			xpathStep(b, "SELECT xpath('/r','<r/>'::xml,NULL::text[]) AS v", "_XML", nil),
		} {
			fixture.SQLJSONBoth(t, p, b, c, s)
		}
		bag := xpathStep(b, `SELECT v::text AS v FROM unnest(xpath('/r/a','<r><a>x</a><a>y</a></r>'::xml)) AS t(v)`, "TEXT", "<a>x</a>", "<a>y</a>")
		bag.Bag = true
		fixture.SQLJSONBoth(t, p, b, c, bag)
		for _, tc := range []struct{ path, want string }{{"false()", "false"}, {"0", "0"}, {`""`, ""}} {
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton(tc.path, "<r/>", "'{}'::text[]"), "TEXT", tc.want))
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT xpath_exists("+xpathSQL(tc.path)+",'<r/>'::xml,'{}'::text[]) AS v", "BOOL", true))
		}
		d := xpathError("22000", "invalid array for XML namespace mapping", "The array must be two-dimensional with length of the second axis equal to 2.", 0)
		xpathFailure(t, p, b, c, "SELECT xpath('/r','<r/>'::xml,ARRAY['q','urn:x'])", d, false)
		d = xpathError("10608", "invalid XPath expression", "Invalid expression", 0)
		xpathFailure(t, p, b, c, "SELECT xpath('[','<r/>'::xml,'{}'::text[])", d, false)
		d.Context = `SQL function "xpath" statement 1`
		xpathFailure(t, p, b, c, "SELECT xpath('[','<r/>'::xml)", d, false)
		q := "SELECT xpath($1::text,$2::xml,'{}'::text[]) AS v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		runner := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		failure := fixture.ErrorDiagnostic(q, xpathError("10608", "invalid XPath expression", "Invalid expression", 0), "[", "<r/>")
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, failure)
		success := xpathArrayStep(q, b.PG, "{<r/>}")
		success.Args = []any{"/r", "<r/>"}
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, success)
		st.Close()
		xpathLifecycle(t, p, b, c)
	})
}
