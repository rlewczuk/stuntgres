package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func xmlPredicateLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func TestReferenceXMLDocumentPredicates(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			s        string
			document bool
		}{
			{"<a/>", true}, {"<a/><b/>", false}, {"abc", false}, {"", false}, {"<!--c-->", false}, {"<!--c--><a/><?p v?>", true}, {" \n<a/>\t", true}, {`<?xml version="1.0"?><a/>`, true}, {`<!DOCTYPE a [<!ELEMENT a EMPTY>]><a/>`, true},
		} {
			source := "XMLPARSE(CONTENT " + xmlPredicateLiteral(tc.s) + ")"
			for _, op := range []string{" IS DOCUMENT", " IS NOT DOCUMENT"} {
				want := tc.document
				if op == " IS NOT DOCUMENT" {
					want = !want
				}
				xmlBoth(t, p, b, c, xmlSelect("SELECT "+source+op+" AS v", "BOOL", want))
			}
			xmlBoth(t, p, b, c, xmlSelect("SELECT NOT ("+source+" IS DOCUMENT) AS v", "BOOL", !tc.document))
		}
		for _, op := range []string{" IS DOCUMENT", " IS NOT DOCUMENT"} {
			xmlBoth(t, p, b, c, xmlSelect("SELECT NULL::xml"+op+" AS v", "BOOL", nil))
		}
		xmlBoth(t, p, b, c, xmlSelect("SELECT (NULL::xml IS DOCUMENT) IS NULL AS v", "BOOL", true))
		xmlBoth(t, p, b, c, xmlSelect("SELECT x IS DOCUMENT AS v FROM (VALUES(XMLPARSE(CONTENT '<a/>')),(XMLPARSE(CONTENT '<a/><b/>')),(NULL::xml)) AS t(x) WHERE x IS DOCUMENT", "BOOL", true))
		xmlFailure(t, p, b, c, "SELECT XMLPARSE(CONTENT '<a>') IS DOCUMENT", xmlError("2200N", "invalid XML content", xmlBadA, 0), false)
	})
}
func TestReferenceXMLWellFormedPredicates(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			s                 string
			document, content bool
		}{
			{"<a/>", true, true}, {"<a/><b/>", false, true}, {"abc", false, true}, {"", false, true}, {"<!--c-->", false, true}, {"<>", false, false}, {"<a>", false, false}, {"<a>&missing;</a>", false, false}, {"<a></b>", false, false}, {"<p:a></p:b>", false, false}, {`<?xml version="1.0"`, false, false}, {`<r xmlns="&lt;"/>`, true, true},
		} {
			for _, mode := range []string{"CONTENT", "DOCUMENT"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION "+mode, 0))
				for _, name := range []string{"xml_is_well_formed", "xml_is_well_formed_document", "xml_is_well_formed_content"} {
					want := tc.document
					if name == "xml_is_well_formed_content" || name == "xml_is_well_formed" && mode == "CONTENT" {
						want = tc.content
					}
					xmlBoth(t, p, b, c, xmlSelect("SELECT "+name+"("+xmlPredicateLiteral(tc.s)+") AS v", "BOOL", want))
				}
			}
		}
		for _, name := range []string{"xml_is_well_formed", "xml_is_well_formed_document", "xml_is_well_formed_content"} {
			xmlBoth(t, p, b, c, xmlSelect("SELECT "+name+"(NULL::text) AS v", "BOOL", nil))
		}
	})
}
func TestReferenceXMLPredicateXMLoption(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT xml_is_well_formed($1) AS v,xml_is_well_formed_document($1) AS d,xml_is_well_formed_content($1) AS c"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION "+mode, 0))
			s := fixture.Select(q, []string{"v", "d", "c"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{mode == "CONTENT", false, true}}, "<a/><b/>")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
		}
		typed := "SELECT $1::xml IS DOCUMENT AS v"
		held, err := c.PrepareContext(p.Ctx, typed)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION "+mode, 0))
			s := xmlSelect(typed, "BOOL", false)
			s.Args = []any{"<a/><b/>"}
			if mode == "DOCUMENT" {
				d := xmlError("2200M", "invalid XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^", 0)
				d.Context = "unnamed portal parameter $1 = '...'"
				s = fixture.ErrorDiagnostic(typed, d, "<a/><b/>")
				s.SelectRows = true
				s.ErrorStage = "execute"
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: typed}, b.Recorder, s)
		}
	})
}
