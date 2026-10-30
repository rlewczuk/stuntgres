package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLPredicateDiagnostics(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ path, message, detail string }{
			{"", "empty XPath expression", ""}, {"[", "invalid XPath expression", "Invalid expression"}, {"$missing", "could not create XPath object", "Undefined variable"}, {"/p:r", "could not create XPath object", "Undefined namespace prefix"}, {"/p:*", "could not create XPath object", "Undefined namespace prefix"}, {`count("x")`, "could not create XPath object", "Invalid type"}, {"unknown()", "could not create XPath object", "Unregistered function"}, {"count()", "could not create XPath object", "Invalid number of arguments"}, {"concat('x')", "could not create XPath object", "Invalid number of arguments"}, {"1|/r", "could not create XPath object", "Invalid type"},
			{"$p:missing", "could not create XPath object", ""}, {"p:missing()", "could not create XPath object", ""}, {"xml:missing()", "could not create XPath object", "Unregistered function"}, {"unknown($missing)", "could not create XPath object", "Undefined variable"}, {"count($missing,1)", "could not create XPath object", "Undefined variable"},
		} {
			xmlFailure(t, p, b, c, xmlExistsQuery(tc.path, "<r/>"), xmlError("10608", tc.message, tc.detail, 0), false)
		}
		for _, tc := range []struct{ document, detail string }{
			{"<a/><b/>", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^"},
			{"text", "line 1: Start tag expected, '<' not found\ntext\n^"},
			{"<!--c-->", "line 1: Start tag expected, '<' not found\n<!--c-->\n        ^"},
			{"", "line 1: Document is empty\n\n^"},
			{"<p:r/>", "line 1: Namespace prefix p on r is not defined\n<p:r/>\n    ^"},
			{`<r xmlns="&lt;"/>`, "line 1: xmlns: '<' is not a valid URI\n<r xmlns=\"&lt;\"/>\n               ^"},
			{`<r xml:id="a b"/>`, "line 1: xml:id : attribute value a b is not an NCName\n<r xml:id=\"a b\"/>\n               ^"},
		} {
			xmlFailure(t, p, b, c, xmlExistsQuery("false()", tc.document), xmlError("2200M", "could not parse XML document", tc.detail, 0), false)
		}
		xmlBoth(t, p, b, c, xmlSelect(`SELECT xml_is_well_formed_document('<r xml:id="a b"/>') AS v`, "BOOL", true))
		xmlBoth(t, p, b, c, xmlSelect(xmlExistsQuery("false()", `<r xml:id="a"/>`), "BOOL", true))
		xmlFailure(t, p, b, c, xmlExistsQuery("", "<a/><b/>"), xmlError("10608", "empty XPath expression", "", 0), false)
		xmlFailure(t, p, b, c, xmlExistsQuery("[", "<a/><b/>"), xmlError("2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^", 0), false)
		for _, mode := range []string{"CONTENT", "DOCUMENT"} {
			code, message := "2200N", "invalid XML content"
			if mode == "DOCUMENT" {
				code, message = "2200M", "invalid XML document"
			}
			xmlFailure(t, p, b, c, "SELECT XMLEXISTS('' PASSING XMLPARSE("+mode+" '<a>'))", xmlError(code, message, xmlBadA, 0), false)
		}
		xmlBoth(t, p, b, c, xmlSelect(xmlExistsQuery("/r", "<r/>"), "BOOL", true))
	})
	t.Run("write-atomicity", func(t *testing.T) {
		xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE SEQUENCE xml_predicate_atomic_seq START WITH 1", 0),
				fixture.Exec("CREATE TABLE xml_predicate_atomic(id integer,v boolean)", 0),
				fixture.Exec("INSERT INTO xml_predicate_atomic VALUES(10,false),(20,false)", 2),
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			// Invocation order, not row identity or scan order, forces one valid
			// predicate evaluation before the second evaluation fails.
			predicate := "XMLEXISTS((CASE WHEN nextval('xml_predicate_atomic_seq')=1 THEN '/r' ELSE '[' END) PASSING '<r/>')"
			unchanged := fixture.Select("SELECT id,v FROM xml_predicate_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(10), false}, {int64(20), false}})
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{
					"INSERT INTO xml_predicate_atomic SELECT id," + predicate + " FROM (VALUES(1),(2)) AS candidate(id)",
					"UPDATE xml_predicate_atomic SET v=" + predicate,
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE xml_predicate_atomic_seq RESTART WITH 1", 0))
					failure := fixture.ErrorDiagnostic(q, xmlError("10608", "invalid XPath expression", "Invalid expression", 0))
					failure.Prepared, failure.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
					xmlBoth(t, p, b, c, xmlSelect("SELECT last_value AS v FROM xml_predicate_atomic_seq", "INT8", int64(2)))
					xmlBoth(t, p, b, c, unchanged)
				}
			}
		})
	})
}
func TestReferenceXMLPredicateInvalidSQL(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, token string }{
			{`SELECT XMLEXISTS('/r' '<r/>')`, "'<r/>'"}, {`SELECT XMLEXISTS('/r' PASSING '<r/>' BY)`, ")"}, {`SELECT XMLEXISTS('/r' PASSING '<r/>','<x/>')`, ","}, {`SELECT XMLEXISTS('/r' PASSING '<r/>') trailing junk`, "junk"},
			{`SELECT XMLEXISTS('/r'::text PASSING XMLPARSE(CONTENT '<r/>'))`, "::"},
			{`SELECT XMLEXISTS('/r' PASSING '<r/>'::xml)`, "::"},
			{`SELECT XMLEXISTS('/' || 'r' PASSING XMLPARSE(CONTENT '<r/>'))`, "||"},
		} {
			raw := tc.token
			pos := strings.Index(tc.q, raw) + 1
			xmlFailure(t, p, b, c, tc.q, xmlError("42601", "syntax error at or near \""+raw+"\"", "", pos), true)
		}
		q := "SELECT 1 IS DOCUMENT"
		xmlFailure(t, p, b, c, q, xmlError("42804", "argument of IS DOCUMENT must be type xml, not type integer", "", 8), true)
		xmlFailure(t, p, b, c, "SELECT 'é', 1 IS DOCUMENT", xmlError("42804", "argument of IS DOCUMENT must be type xml, not type integer", "", 13), true)
		q = "SELECT xml_is_well_formed(1)"
		d := xmlError("42883", "function xml_is_well_formed(integer) does not exist", "", 8)
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		xmlFailure(t, p, b, c, q, d, true)
	})
	t.Run("execute-authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"xml_predicate_reader"})
		xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE ROLE xml_predicate_reader", "REVOKE EXECUTE ON FUNCTION pg_catalog.xml_is_well_formed(text) FROM PUBLIC", "REVOKE EXECUTE ON FUNCTION pg_catalog.xmlexists(text,xml) FROM PUBLIC", "SET ROLE xml_predicate_reader"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			defer func() {
				for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.xml_is_well_formed(text) TO PUBLIC", "GRANT EXECUTE ON FUNCTION pg_catalog.xmlexists(text,xml) TO PUBLIC"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			for _, tc := range []struct{ q, name string }{{"SELECT pg_catalog.xml_is_well_formed('<r/>')", "xml_is_well_formed"}, {"SELECT pg_catalog.xmlexists('/r','<r/>'::xml)", "xmlexists"}} {
				xmlFailure(t, p, b, c, tc.q, xmlError("42501", "permission denied for function "+tc.name, "", 0), false)
			}
			xmlFailure(t, p, b, c, "SELECT XMLEXISTS('/r' PASSING ('<r/>'::xml)) AS v", xmlError("42501", "permission denied for function xmlexists", "", 0), false)
		})
	})
}
