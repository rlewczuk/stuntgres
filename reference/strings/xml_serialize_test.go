package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLSerialize(t *testing.T) {
	xmlSerializeAcceptance(t)
	t.Run("catalog-target", xmlSerializeCatalogTarget)
	t.Run("catalog-source", xmlSerializeCatalogSource)
}

func xmlSerializeAcceptance(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN xml_text_target AS text", 0))
		for _, tc := range []struct {
			expr, typ string
			value     any
		}{
			{"XMLSERIALIZE(CONTENT '<a/>' AS text)", "TEXT", "<a/>"},
			{"XMLSERIALIZE(CONTENT '<a/>'::xml AS name)", "NAME", "<a/>"},
			{"XMLSERIALIZE(CONTENT '<a/>'::xml AS xml_text_target)", "TEXT", "<a/>"},
			{"XMLSERIALIZE(CONTENT '<a/>'::xml AS character(6))", "BPCHAR", "<a/>  "},
			{"XMLSERIALIZE(CONTENT '<a/>'::xml AS varchar(9))", "VARCHAR", "<a/>"},
			{"XMLSERIALIZE(DOCUMENT NULL::xml AS varchar(2) INDENT)", "VARCHAR", nil},
			{"XMLSERIALIZE(CONTENT NULL AS text)", "TEXT", nil},
			{"'<abcdef/>'::xml::varchar(3)", "VARCHAR", "<ab"},
			{"XMLSERIALIZE(CONTENT '<a><b/></a>'::xml AS text NO INDENT)", "TEXT", "<a><b/></a>"},
			{"XMLSERIALIZE(CONTENT '<a>before<b/>after</a>'::xml AS text INDENT)", "TEXT", "<a>before<b/>after</a>"},
		} {
			s := xmlSelect("SELECT "+tc.expr+" AS v", tc.typ, tc.value)
			if tc.typ == "VARCHAR" {
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					ts, e := r.ColumnTypes()
					if e != nil {
						t.Fatal(e)
					}
					n, ok := ts[0].Length()
					want := int64(9)
					if tc.value == nil {
						want = 2
					}
					if tc.expr == "'<abcdef/>'::xml::varchar(3)" {
						want = 3
					}
					if n != want || !ok {
						t.Fatalf("varchar length %d/%v want %d", n, ok, want)
					}
				}
			}
			xmlBoth(t, p, b, c, s)
		}
		for _, mode := range []string{"DOCUMENT", "CONTENT"} {
			xmlBoth(t, p, b, c, xmlSelect("SELECT XMLSERIALIZE("+mode+" '<a><b/></a>'::xml AS text INDENT) AS v", "TEXT", "<a>\n  <b/>\n</a>"))
		}
		// Genuine entity substitution and DTD default attributes, not lexical indentation.
		xmlBoth(t, p, b, c, xmlSelect(`SELECT XMLSERIALIZE(DOCUMENT XMLPARSE(DOCUMENT '<!DOCTYPE a [<!ELEMENT a (#PCDATA)><!ATTLIST a kind CDATA "default"><!ENTITY e "hello">]><a>&e;</a>') AS text INDENT) AS v`, "TEXT", "<!DOCTYPE a [\n<!ELEMENT a (#PCDATA)>\n<!ATTLIST a kind CDATA \"default\">\n<!ENTITY e \"hello\">\n]>\n<a kind=\"default\">hello</a>"))
		for _, tc := range []struct {
			q, code, message, detail string
			pos                      int
			analysis                 bool
		}{
			{"SELECT XMLSERIALIZE(DOCUMENT 'a<b/>'::xml AS text)", "2200L", "not an XML document", "", 0, false},
			{"SELECT XMLSERIALIZE(CONTENT '<a/>'::text AS text)", "42804", "argument of XMLSERIALIZE must be type xml, not type text", "", 29, true},
			{"SELECT XMLSERIALIZE(CONTENT 42 AS text)", "42804", "argument of XMLSERIALIZE must be type xml, not type integer", "", 29, true},
			{"SELECT XMLSERIALIZE(CONTENT '<a>' AS integer)", "2200N", "invalid XML content", xmlBadA, 29, true},
			{"SELECT XMLSERIALIZE(CONTENT '<a>'::text AS integer)", "42804", "argument of XMLSERIALIZE must be type xml, not type text", "", 29, true},
			{"SELECT XMLSERIALIZE(CONTENT '<abcdef/>'::xml AS varchar(3))", "22001", "value too long for type character varying(3)", "", 0, false},
			{"SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS char)", "22001", "value too long for type character(1)", "", 0, false},
			{"SELECT XMLSERIALIZE(CONTENT NULL AS text[])", "42601", `syntax error at or near "["`, "", 41, true},
			{"SELECT XMLSERIALIZE(CONTENT NULL AS varchar(0))", "22023", "length for type varchar must be at least 1", "", 37, true},
			{"SELECT XMLSERIALIZE(CONTENT NULL AS text(2))", "42601", `type modifier is not allowed for type "text"`, "", 37, true},
		} {
			xmlFailure(t, p, b, c, tc.q, xmlError(tc.code, tc.message, tc.detail, tc.pos), tc.analysis)
		}
		for _, target := range []string{"integer", "xml", "bytea", `"char"`} {
			xmlFailure(t, p, b, c, "SELECT XMLSERIALIZE(CONTENT NULL AS "+target+")", xmlError("42846", "cannot cast XMLSERIALIZE result to "+target, "", 8), true)
		}
		original := `<?xml version="1.0" encoding="LATIN1"?><a>é</a>`
		for _, mode := range []string{"CONTENT", "DOCUMENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION "+mode, 0))
			s := xmlSelect("SELECT $1::xml AS v", "XML", "<a>é</a>")
			s.Args = []any{original}
			xmlBoth(t, p, b, c, s)
			for _, expr := range []string{"$1::xml::text", "XMLSERIALIZE(CONTENT $1::xml AS text NO INDENT)"} {
				s := xmlSelect("SELECT "+expr+" AS v", "TEXT", original)
				s.Args = []any{original}
				xmlBoth(t, p, b, c, s)
			}
		}
	})
}

func xmlSerializeCatalogTarget(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		functionCreated, castCreated := false, false
		defer func() {
			if castCreated {
				if _, e := c.ExecContext(p.Ctx, "DROP CAST (text AS integer)"); e != nil {
					t.Error(e)
				}
			}
			if functionCreated {
				if _, e := c.ExecContext(p.Ctx, "DROP FUNCTION public.xml_target(text)"); e != nil {
					t.Error(e)
				}
			}
		}()
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE FUNCTION public.xml_target(text) RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT 7'", 0))
		functionCreated = true
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE CAST (text AS integer) WITH FUNCTION public.xml_target(text) AS IMPLICIT", 0))
		castCreated = true
		// The registered nonSTRICT implicit TEXT cast runs even on NULL
		// serialization text. PostgreSQL independently returns integer7 twice.
		q := "SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS integer), XMLSERIALIZE(CONTENT NULL::xml AS integer)"
		xmlBoth(t, p, b, c, fixture.Select(q, []string{"xmlserialize", "xmlserialize"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}))
		if len(notices) != 0 {
			t.Fatalf("catalog XML target emitted notices %#v", notices)
		}
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("DROP CAST (text AS integer)", 0))
		castCreated = false
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("DROP FUNCTION public.xml_target(text)", 0))
		functionCreated = false
		if len(notices) != 0 {
			t.Fatalf("catalog XML target cleanup emitted notices %#v", notices)
		}
	})
}

func xmlSerializeCatalogSource(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		functionCreated, castCreated := false, false
		defer func() {
			if castCreated {
				if _, e := c.ExecContext(p.Ctx, "DROP CAST (integer AS xml)"); e != nil {
					t.Error(e)
				}
			}
			if functionCreated {
				if _, e := c.ExecContext(p.Ctx, "DROP FUNCTION public.xml_source_xml(integer)"); e != nil {
					t.Error(e)
				}
			}
		}()
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE FUNCTION public.xml_source_xml(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(DOCUMENT ''<xml-source/>'')'", 0))
		functionCreated = true
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE CAST (integer AS xml) WITH FUNCTION public.xml_source_xml(integer) AS ASSIGNMENT", 0))
		castCreated = true
		// XMLSERIALIZE source admission obeys the ordinary assignment cast,
		// including its nonSTRICT conversion of the NULL integer argument.
		q := "SELECT XMLSERIALIZE(CONTENT 42 AS text), XMLSERIALIZE(CONTENT NULL::integer AS text)"
		xmlBoth(t, p, b, c, fixture.Select(q, []string{"xmlserialize", "xmlserialize"}, []string{"TEXT", "TEXT"}, [][]any{{"<xml-source/>", "<xml-source/>"}}))
		if len(notices) != 0 {
			t.Fatalf("XML serializer source emitted notices %#v", notices)
		}
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("DROP CAST (integer AS xml)", 0))
		castCreated = false
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("DROP FUNCTION public.xml_source_xml(integer)", 0))
		functionCreated = false
		if len(notices) != 0 {
			t.Fatalf("XML serializer source cleanup emitted notices %#v", notices)
		}
	})
}
