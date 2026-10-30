package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

const xmlPredicateR = `<r><a id="1">x</a><a id="2">y</a></r>`

func xmlExistsQuery(path, document string) string {
	return "SELECT XMLEXISTS(" + xmlPredicateLiteral(path) + " PASSING XMLPARSE(CONTENT " + xmlPredicateLiteral(document) + ")) AS v"
}
func xmlExistsCases(t *testing.T, document string, cases []struct {
	path string
	want bool
}) {
	t.Helper()
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			xmlBoth(t, p, b, c, xmlSelect(xmlExistsQuery(tc.path, document), "BOOL", tc.want))
		}
	})
}
func TestReferenceXMLExistsPredicates(t *testing.T) {
	xmlExistsCases(t, xmlPredicateR, []struct {
		path string
		want bool
	}{
		{"/r/a", true}, {"r/a", true}, {"/r/missing", false}, {"a", false}, {`/r/a[@id="2"][text()="y"]`, true}, {`/r/a[@id="3"]`, false}, {`/r/a[position()=last()][@id="2"]`, true}, {"false()", true}, {"0", true}, {"0 div 0", true}, {`""`, true}, {"count(/missing)", true},
	})
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION CONTENT", 0))
		for _, q := range []string{"SELECT XMLEXISTS('/r' PASSING (NULL::xml)) AS v", "SELECT XMLEXISTS((NULL::text) PASSING ('<r/>'::xml)) AS v", "SELECT XMLEXISTS((NULL::text) PASSING (NULL::xml)) AS v", "SELECT XMLEXISTS('[' PASSING (NULL::xml)) AS v"} {
			xmlBoth(t, p, b, c, xmlSelect(q, "BOOL", nil))
		}
		q := "SELECT XMLEXISTS($1 PASSING $2) AS v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		runner := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, tc := range []struct {
			path, doc string
			want      bool
		}{{"/r/a", xmlPredicateR, true}, {"/r/missing", "<r/>", false}, {"false()", "<r/>", true}} {
			s := xmlSelect(q, "BOOL", tc.want)
			s.Args = []any{tc.path, tc.doc}
			fixture.RunStep(t, p.Ctx, runner, b.Recorder, s)
		}
		failure := fixture.ErrorDiagnostic(q, xmlError("10608", "invalid XPath expression", "Invalid expression", 0), "[", "<r/>")
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, failure)
		fragment := fixture.ErrorDiagnostic(q, xmlError("2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^", 0), "/r", "<a/><b/>")
		fragment.SelectRows = true
		fragment.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, fragment)
		inputError := xmlError("2200N", "invalid XML content", xmlBadA, 0)
		inputError.Context = "unnamed portal parameter $2 = '...'"
		malformed := fixture.ErrorDiagnostic(q, inputError, "/r", "<a>")
		malformed.SelectRows = true
		malformed.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, malformed)
		success := xmlSelect(q, "BOOL", true)
		success.Args = []any{"/r", "<r/>"}
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, success)
	})
}
func TestReferenceXMLExistsPassing(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, before := range []string{"", " BY REF", " BY VALUE"} {
			for _, after := range []string{"", " BY REF", " BY VALUE"} {
				for _, tc := range []struct {
					path, doc string
					want      any
				}{{"'/r/a'", "'<r><a/></r>'::xml", true}, {"'/r/missing'", "'<r/>'::xml", false}, {"NULL::text", "'<r/>'::xml", nil}, {"'/r'", "NULL::xml", nil}} {
					q := "SELECT XMLEXISTS((" + tc.path + ") PASSING" + before + " (" + tc.doc + ")" + after + ") AS v"
					xmlBoth(t, p, b, c, xmlSelect(q, "BOOL", tc.want))
				}
				view := "CREATE VIEW xml_passing_view AS SELECT XMLEXISTS('/r' PASSING" + before + " ('<r/>'::xml)" + after + ") AS v,XMLEXISTS('/missing' PASSING" + before + " ('<r/>'::xml)" + after + ") AS f,XMLEXISTS((NULL::text) PASSING" + before + " ('<r/>'::xml)" + after + ") AS n,XMLEXISTS('/r' PASSING" + before + " (NULL::xml)" + after + ") AS x"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(view, 0))
				xmlBoth(t, p, b, c, fixture.Select("SELECT v,f,n,x FROM xml_passing_view", []string{"v", "f", "n", "x"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, nil, nil}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP VIEW xml_passing_view", 0))
			}
		}
		xmlBoth(t, p, b, c, xmlSelect("SELECT pg_catalog.xmlexists('/r','<r/>'::xml) AS v", "BOOL", true))
		xmlBoth(t, p, b, c, xmlSelect(`SELECT "xmlexists"('/missing','<r/>'::xml) AS v`, "BOOL", false))
		xmlBoth(t, p, b, c, xmlSelect("SELECT XMLEXISTS(CAST('/r' AS text) PASSING CAST('<r/>' AS xml)) AS v", "BOOL", true))
		xmlBoth(t, p, b, c, xmlSelect("SELECT XMLEXISTS(('/' || 'r') PASSING XMLPARSE(CONTENT '<r/>')) AS v", "BOOL", true))
	})
}
