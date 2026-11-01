package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func xpathArrayStep(q string, pg bool, values ...any) fixture.Step {
	return xpathStep(fixture.Backend{PG: pg}, q, "_XML", values...)
}
func TestReferenceXMLXPathArrays(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			want any
		}{
			{"SELECT xpath('/missing','<r/>'::xml) AS v", "{}"},
			{"SELECT xpath('\"\"','<r/>'::xml) AS v", `{""}`},
			{"SELECT NULL::xml[] AS v", nil},
			{"SELECT '{NULL}'::xml[] AS v", "{NULL}"},
			{"SELECT xpath('/r','<r/>'::xml) AS v", "{<r/>}"},
			{"SELECT '[0:1][-1:0]={{<r/>,NULL},{\"\",text}}'::xml[] AS v", `[0:1][-1:0]={{<r/>,NULL},{"",text}}`},
		} {
			fixture.SQLJSONBoth(t, p, b, c, xpathArrayStep(tc.q, b.PG, tc.want))
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE xpath_arrays(id integer,v xml[])", 0),
			fixture.Exec("INSERT INTO xpath_arrays VALUES(1,xpath('/r','<r/>'::xml)),(2,'{NULL}'::xml[]),(3,'{}'::xml[]),(4,NULL)", 4),
			fixture.Exec("INSERT INTO xpath_arrays VALUES(5,$1::xml[])", 1, `[0:0][-1:0]={{"<a q=\"v\"/>",NULL}}`),
			fixture.Exec("CREATE DOMAIN xpath_xml_array AS xml[]", 0),
			fixture.Exec("CREATE DOMAIN xpath_xml_element AS xml", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.SQLJSONBoth(t, p, b, c, xpathStoredArrayStep("SELECT v FROM xpath_arrays ORDER BY id", b.PG, "{<r/>}", "{NULL}", "{}", nil, `[0:0][-1:0]={{"<a q=\"v\"/>",NULL}}`))
		fixture.SQLJSONBoth(t, p, b, c, xpathArrayStep("SELECT xpath('/r','<r/>'::xml) AS v WHERE false", b.PG))
		fixture.SQLJSONBoth(t, p, b, c, xpathStoredArrayStep("SELECT v FROM xpath_arrays WHERE false", b.PG))
		fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT ('{<r/>}'::xpath_xml_array)::xml[]::text AS v", "TEXT", "{<r/>}"))
		fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT ('{<r/>,NULL}'::xpath_xml_element[])::text AS v", "TEXT", "{<r/>,NULL}"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION DOCUMENT", 0))
		fixture.SQLJSONBoth(t, p, b, c, xpathArrayStep("SELECT xpath('\"\"','<r/>'::xml) AS v", b.PG, `{""}`))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET xmloption", 0))
		d := xpathError("2200N", "invalid XML content", xpathBadA, 0)
		d.Context = "unnamed portal parameter $1 = '...'"
		xpathFailure(t, p, b, c, `SELECT '{<r/>}'::xml[] = '{<r/>}'::xml[]`, xpathError("42883", "could not identify an equality operator for type xml", "", 0), false)
		xpathFailure(t, p, b, c, "SELECT $1::xml[]", d, false, `{<a>}`)
		fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT count(*) AS v FROM xpath_arrays", "INT8", int64(5)))
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select("SELECT oid::bigint AS oid,typelem::bigint AS element,typcollation::bigint AS collation FROM pg_type WHERE oid=143", []string{"oid", "element", "collation"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(143), int64(142), int64(0)}}))
	})
}
