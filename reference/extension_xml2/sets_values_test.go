package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2SetsValues(t *testing.T) {
	xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		xml2Install(t, p, b, c)
		for _, call := range xml2SetCalls() {
			t.Run(call.name, func(t *testing.T) {
				q := "SELECT " + call.expression + " AS value"
				s := xml2SetValue(q, call.healthy, call.args...)
				xml2Both(t, p, b, c, s)
				missing := append([]any(nil), call.args...)
				missing[1] = "/missing"
				empty := ""
				if call.name == "nodes4" {
					empty = "<o></o>"
				}
				xml2Held(t, p, b, c, s, xml2SetValue(q, empty, missing...), s)
			})
		}
		for _, tc := range []struct {
			expr, doc, path, want string
		}{
			{"xpath_nodeset($1,$2,'o','')", xml2SetsDocument, "/r/a", "<o>" + xml2SetsNodes + "</o>"},
			{"xpath_nodeset($1,$2,'','')", xml2SetsDocument, "/r/a", xml2SetsNodes},
			{"xpath_list($1,$2,',')", xml2SetsDocument, "/r/a", "ABC,D&E"},
			{"xpath_list($1,$2,'')", xml2SetsDocument, "/r/a", "ABCD&E"},
			{"xpath_nodeset($1,$2)", "<r><a/><a>X</a><a/></r>", "/r/a", "<a/><a>X</a><a/>"},
			{"xpath_list($1,$2)", "<r><a/><a>X</a><a/></r>", "/r/a", ",X,"},
			{"xpath_list($1,$2,'雪&')", "<r><a/><a>X</a><a/></r>", "/r/a", "雪&X雪&"},
			{"xpath_nodeset($1,$2)", "<r><a/></r>", "/r/a", "<a/>"},
			{"xpath_list($1,$2)", "<r><a/></r>", "/r/a", ""},
			{"xpath_nodeset($1,$2)", "<r><a>A</a></r>", "a", "<a>A</a>"},
			{"xpath_list($1,$2)", "<r><a>A</a></r>", "a", "A"},
			{"xpath_nodeset($1,$2)", "<r><a>A</a></r>", "r/a", ""},
			{"xpath_list($1,$2)", "<r><a>A</a></r>", "r/a", ""},
			{"xpath_nodeset($1,$2,'o','i')", xml2SetsDocument, "string(.)", "ABCD&E"},
			{"xpath_list($1,$2,'|')", xml2SetsDocument, "string(.)", "ABCD&E"},
			{"xpath_nodeset($1,$2,'o','i')", "<r/>", "'<&'", "<&"},
			{"xpath_list($1,$2,'|')", "<r/>", "'<&'", "<&"},
		} {
			xml2Both(t, p, b, c, xml2SetValue("SELECT "+tc.expr+" AS value", tc.want, tc.doc, tc.path))
		}
		xml2SetsRows(t, p, b, c)
	})
}

func xml2SetsRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE xml2_sets_source(id integer,doc text,path text)", 0),
		fixture.Exec("INSERT INTO xml2_sets_source VALUES(1,$1,'/r/a'),(2,'<r/>','/missing'),(3,NULL,'[')", 3, xml2SetsDocument),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	q := "SELECT id,xpath_nodeset(doc,path) AS nodes,xpath_list(doc,path) AS list FROM xml2_sets_source"
	for _, tc := range []struct {
		suffix string
		rows [][]any
	}{
		{" ORDER BY id", [][]any{{int64(1), xml2SetsNodes, "ABC,D&E"}, {int64(2), "", ""}, {int64(3), nil, nil}}},
		{" WHERE xpath_list(doc,path)='ABC,D&E' ORDER BY id", [][]any{{int64(1), xml2SetsNodes, "ABC,D&E"}}},
		{" WHERE xpath_nodeset(doc,path) IS NULL ORDER BY id", [][]any{{int64(3), nil, nil}}},
		{" WHERE false ORDER BY id", nil},
	} {
		xml2Both(t, p, b, c, fixture.Select(q+tc.suffix, []string{"id", "nodes", "list"}, []string{"INT4", "TEXT", "TEXT"}, tc.rows))
	}
	xml2Both(t, p, b, c, fixture.Select("SELECT xpath_nodeset($1,$2)= $3 AS nodes_equal,xpath_list($1,$2)= $4 AS list_equal", []string{"nodes_equal", "list_equal"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, xml2SetsDocument, "/r/a", xml2SetsNodes, "ABC,D&E"))
}
