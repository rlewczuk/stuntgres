package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Exact source/caret blocks captured from the pinned 18.6 oracle; warning
// domains are deliberately retained in legacy compile Detail, not notices.
var xml2LegacyDiagnostics = []struct {
	doc, message string
	caret        int
}{
	{"<p:r>8</p:r>", "Namespace prefix p on r is not defined", 4},
	{"<r xmlns:xml='urn:wrong' xml:lang='en'>8</r>", "xml namespace prefix mapped to wrong URI", 24},
	{"<r xmlns:p='urn:x' xmlns:q='urn:x' p:a='1' q:a='2'/>", "Namespaced Attribute a in 'urn:x' redefined", 50},
	{"<r xml:id='bad id'>8</r>", "xml:id : attribute value bad id is not an NCName", 18},
	{"<r xml:space='invalid'>8</r>", "Invalid value \"invalid\" for xml:space : \"default\" or \"preserve\" expected", 22},
	{"<?xml version='1.2'?><r>8</r>", "Unsupported version '1.2'", 19},
}

func xml2LegacyEvents(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range xml2LegacyDiagnostics {
		detail := "line 1: " + tc.message + "\n" + tc.doc + "\n" + strings.Repeat(" ", tc.caret) + "^\nInvalid expression"
		for _, fn := range []string{"xpath_string", "xpath_number", "xpath_bool"} {
			xml2Failure(t, p, b, c, "SELECT "+fn+"($1::text,$2::text)", xml2Compile(detail), false, tc.doc, "[")
		}
		// Evaluation failures must not leak accumulated parser events either.
		xml2Both(t, p, b, c, xml2Scalars(tc.doc, "$v", nil, nil, false))
	}
}

func xml2LegacyEncoding(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		doc, text, length string
		n                 float64
	}{
		{"<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><r>é</r>", "Ã©", "2", 2},
		{"<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><r>&#233;</r>", "é", "1", 1},
		{"<?xml version=\"1.0\" encoding=\"UTF-8\"?><r>é</r>", "é", "1", 1},
		{"<r>é</r>", "é", "1", 1},
		{"\ufeff<r>é</r>", "é", "1", 1},
	} {
		xml2Both(t, p, b, c, xml2Scalars(tc.doc, ".", tc.text, nil, true))
		xml2Both(t, p, b, c, xml2Scalars(tc.doc, "string-length(.)", tc.length, tc.n, true))
		xml2Both(t, p, b, c, xml2Scalars(tc.doc, "true()", "true", float64(1), true))
		xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid($1::text) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, tc.doc))
	}
	for _, encoding := range []string{"NO-SUCH-ENCODING", "UTF-16"} {
		doc := "<?xml version=\"1.0\" encoding=\"" + encoding + "\"?><r>é</r>"
		for _, path := range []string{".", "true()", "["} {
			xml2Both(t, p, b, c, xml2Scalars(doc, path, nil, nil, false))
		}
		xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid($1::text) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, doc))
	}
}

func xml2ValidationWarnings(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var notices []fixture.Diagnostic
	_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec("SET xmloption TO CONTENT", 0))
	q := "SELECT xml_valid($1::text) AS valid"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if len(notices) != 0 {
		t.Fatalf("Prepare notices=%#v", notices)
	}
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			doc      string
			warnings []fixture.Diagnostic
		}{
			{"<r xml:space='invalid'>8</r>", []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "line 1: Invalid value \"invalid\" for xml:space : \"default\" or \"preserve\" expected\n<r xml:space='invalid'>8</r>\n                      ^"}}},
			{"<?xml version='1.2'?><r>8</r>", nil},
			{"<r/>", nil},
		} {
			notices = nil
			s := fixture.Select(q, []string{"valid"}, []string{"BOOL"}, [][]any{{true}}, tc.doc)
			if prepared {
				fixture.RunStep(t, p.Ctx, h, recorder, s)
			} else {
				fixture.RunStep(t, p.Ctx, c, recorder, s)
			}
			if !reflect.DeepEqual(notices, tc.warnings) {
				t.Fatalf("xml_valid warnings=%#v want=%#v", notices, tc.warnings)
			}
		}
	}
}
