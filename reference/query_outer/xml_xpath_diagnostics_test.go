package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLXPathDiagnostics(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, name := range []string{"xpath", "xpath_exists"} {
			for _, tc := range []struct{ path, doc, mapping, code, message, detail string }{
				{"[", "<r/>", "'{}'::text[]", "10608", "invalid XPath expression", "Invalid expression"},
				{"/p:r", "<r/>", "'{}'::text[]", "10608", "could not create XPath object", "Undefined namespace prefix"},
				{"$missing", "<r/>", "'{}'::text[]", "10608", "could not create XPath object", "Undefined variable"},
				{"unknown()", "<r/>", "'{}'::text[]", "10608", "could not create XPath object", "Unregistered function"},
				{`count("x")`, "<r/>", "'{}'::text[]", "10608", "could not create XPath object", "Invalid type"},
				{"count()", "<r/>", "'{}'::text[]", "10608", "could not create XPath object", "Invalid number of arguments"},
				{"", "<a/><b/>", "ARRAY['q','urn:x']", "22000", "invalid array for XML namespace mapping", "The array must be two-dimensional with length of the second axis equal to 2."},
				{"", "<r/>", "ARRAY[ARRAY[ARRAY['q','urn:x']]]", "22000", "invalid array for XML namespace mapping", "The array must be two-dimensional with length of the second axis equal to 2."},
				{"", "<r/>", "ARRAY[ARRAY['q','urn:x','extra']]", "22000", "invalid array for XML namespace mapping", "The array must be two-dimensional with length of the second axis equal to 2."},
				{"", "<a/><b/>", "ARRAY[ARRAY[NULL,'urn:x']]", "10608", "empty XPath expression", ""},
				{"[", "<a/><b/>", "ARRAY[ARRAY[NULL,'urn:x']]", "2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^"},
				{"[", "<r/>", "ARRAY[ARRAY[NULL,'urn:x']]", "22004", "neither namespace name nor URI may be null", ""},
				{"[", "<r/>", "ARRAY[ARRAY['q',NULL]]", "22004", "neither namespace name nor URI may be null", ""},
				{"[", "<r/>", "ARRAY[ARRAY['','urn:x']]", "XX000", `could not register XML namespace with name "" and URI "urn:x"`, ""},
				{"/r", "<r/>", "ARRAY[ARRAY['','']]", "XX000", `could not register XML namespace with name "" and URI ""`, ""},
			} {
				q := "SELECT " + name + "(" + xpathSQL(tc.path) + "," + xpathSQL(tc.doc) + "::xml," + tc.mapping + ")"
				xpathFailure(t, p, b, c, q, xpathError(tc.code, tc.message, tc.detail, 0), false)
			}
			for _, tc := range []struct{ document, detail string }{
				{"text", "line 1: Start tag expected, '<' not found\ntext\n^"},
				{"<!--c-->", "line 1: Start tag expected, '<' not found\n<!--c-->\n        ^"},
				{"", "line 1: Document is empty\n\n^"},
				{"<p:r/>", "line 1: Namespace prefix p on r is not defined\n<p:r/>\n    ^"},
				{`<r xmlns="&lt;"/>`, "line 1: xmlns: '<' is not a valid URI\n<r xmlns=\"&lt;\"/>\n               ^"},
				{`<r xml:id="a b"/>`, "line 1: xml:id : attribute value a b is not an NCName\n<r xml:id=\"a b\"/>\n               ^"},
			} {
				xpathFailure(t, p, b, c, "SELECT "+name+"('false()',"+xpathSQL(tc.document)+"::xml,'{}'::text[])", xpathError("2200M", "could not parse XML document", tc.detail, 0), false)
			}
			nullType := "_XML"
			if name == "xpath_exists" {
				nullType = "BOOL"
			}
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT "+name+"(NULL::text,XMLPARSE(CONTENT '<a>')) AS v", nullType, nil))
			input := xpathError("2200N", "invalid XML content", xpathBadA, 0)
			input.Context = "unnamed portal parameter $2 = '...'"
			xpathFailure(t, p, b, c, "SELECT "+name+"($1::text,$2::xml)", input, false, "[", "<a>")
			for _, tc := range []struct{ suffix, signature string }{
				{"()", ""}, {"('/r')", "unknown"}, {"('/r',1)", "unknown, integer"}, {"('/r','<r/>'::xml,1)", "unknown, xml, integer"},
			} {
				q := "/*é*/ SELECT " + name + tc.suffix
				d := xpathError("42883", "function "+name+"("+tc.signature+") does not exist", "", 14)
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				xpathFailure(t, p, b, c, q, d, true)
			}
			upper := "XPATH"
			if name == "xpath_exists" {
				upper = "XPATH_EXISTS"
			}
			for _, tc := range []struct{ spelling, display string }{{`"` + upper + `"`, upper}, {"public." + name, "public." + name}} {
				d := xpathError("42883", "function "+tc.display+"(unknown, xml) does not exist", "", 8)
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				xpathFailure(t, p, b, c, "SELECT "+tc.spelling+"('/r','<r/>'::xml)", d, true)
			}
			typ, want := "_XML", any("{<r/>}")
			if name == "xpath_exists" {
				typ, want = "BOOL", true
			}
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT pg_catalog."+name+"('/r','<r/>'::xml) AS v", typ, want))
		}
	})
}
func TestReferenceXMLXPathWrappers(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, name := range []string{"xpath", "xpath_exists"} {
			for _, generic := range []bool{false, true} {
				setting := "force_custom_plan"
				if generic {
					setting = "force_generic_plan"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET plan_cache_mode="+setting, 0))
				for _, tc := range []struct {
					args, from string
					parameters bool
					wrapper    bool
				}{
					{"'','<r/>'::xml", "", false, true},
					{"'','<r/>'::xml,'{}'::text[]", "", false, false},
					{"$1::text,$2::xml", "", true, !generic},
					{"$1::text,$2::xml,'{}'::text[]", "", true, false},
					{"p,x", " FROM (VALUES (''::text,'<r/>'::xml),('/r','<r/>'::xml)) t(p,x)", false, false},
					{"'',x", " FROM (VALUES ('<r/>'::xml),('<s/>'::xml)) t(x)", false, false},
					{"$1::text,x", " FROM (VALUES ('<r/>'::xml),('<s/>'::xml)) t(x)", true, false},
				} {
					q := "SELECT " + name + "(" + tc.args + ")" + tc.from
					d := xpathError("10608", "empty XPath expression", "", 0)
					if tc.wrapper {
						d.Context = "SQL function \"" + name + "\" statement 1"
					}
					var args []any
					if tc.parameters {
						args = []any{""}
						if tc.from == "" {
							args = append(args, "<r/>")
						}
					}
					xpathFailure(t, p, b, c, q, d, false, args...)
				}
			}
		}
	})
}
func TestReferenceXMLXPathNullsAndReuse(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, name := range []string{"xpath", "xpath_exists"} {
			typ := "_XML"
			if name == "xpath_exists" {
				typ = "BOOL"
			}
			for _, args := range []string{"NULL::text,'<r/>'::xml", "'[',NULL::xml", "'/r','<r/>'::xml,NULL::text[]", "NULL::text,NULL::xml,NULL::text[]"} {
				fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT "+name+"("+args+") AS v", typ, nil))
			}
			q := "SELECT " + name + "($1::text,$2::xml,$3::text[]) AS v"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			runner := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			for _, tc := range []struct {
				path    string
				mapping any
				want    any
			}{
				{"/q:r", "{{q,urn:x}}", `{"<r xmlns=\"urn:x\"/>"}`},
				{"/q:r", "{{q,urn:y}}", "{}"},
				{"[", nil, nil},
				{"/q:r", "{{q,urn:x}}", `{"<r xmlns=\"urn:x\"/>"}`},
			} {
				want := tc.want
				if name == "xpath_exists" && want != nil {
					want = tc.path == "/q:r" && tc.mapping == "{{q,urn:x}}"
				}
				s := xpathStep(b, q, typ, want)
				s.Args = []any{tc.path, `<r xmlns="urn:x"/>`, tc.mapping}
				fixture.RunStep(t, p.Ctx, runner, b.Recorder, s)
			}
			failure := fixture.ErrorDiagnostic(q, xpathError("10608", "invalid XPath expression", "Invalid expression", 0), "[", "<r/>", "{}")
			failure.SelectRows = true
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, runner, b.Recorder, failure)
			s := xpathStep(b, q, typ, "{}")
			if name == "xpath_exists" {
				s.Rows = [][]any{{false}}
			}
			s.Args = []any{"/missing", "<r/>", "{}"}
			fixture.RunStep(t, p.Ctx, runner, b.Recorder, s)
			st.Close()
			q = "SELECT " + name + "($1::text,$2::xml) AS v"
			two, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: two, SQL: q}
			for _, path := range []any{"/r", "[", nil, "/r"} {
				if path == "[" {
					d := xpathError("10608", "invalid XPath expression", "Invalid expression", 0)
					d.Context = "SQL function \"" + name + "\" statement 1"
					bad := fixture.ErrorDiagnostic(q, d, path, "<r/>")
					bad.SelectRows = true
					bad.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
				} else {
					want := any("{<r/>}")
					if name == "xpath_exists" {
						want = true
					}
					if path == nil {
						want = nil
					}
					good := xpathStep(b, q, typ, want)
					good.Args = []any{path, "<r/>"}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				}
			}
			two.Close()
		}
	})
}
