package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2LegacyParsing(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct {
			doc, path string
			s, n, b   any
		}{
			{"<r xmlns=\"urn:r\"><a>8</a></r>", "/r/a", "", nil, false},
			{"<r xmlns=\"urn:r\"><a>8</a></r>", "/*[local-name()='r' and namespace-uri()='urn:r']/*[local-name()='a']", "8", float64(8), true},
			{"<p:r xmlns:p=\"urn:r\"/>", "/p:r", nil, nil, false},
			{"<p:r xmlns:p=\"urn:r\">8</p:r>", "/*", "8", float64(8), true},
			{"<r/>", "$v", nil, nil, false},
			{"<r/>", "unregistered()", nil, nil, false},
			{"<r xml:lang=\"en\"/>", "@xml:lang", "en", nil, true},
			{"<r/>", "namespace::xml", "http://www.w3.org/XML/1998/namespace", nil, true},
			{"<r xmlns=\"urn:r\"><a xmlns=\"\">8</a></r>", "/*/a", "8", float64(8), true},
			{"<?xml version=\"1.0\" encoding=\"UTF-8\"?><r>é</r>", ".", "é", nil, true},
			{"<r>&#233;</r>", ".", "é", nil, true},
			{"<p:r>8</p:r>", "/*", "8", float64(8), true},
			{"<p:r>8</p:r>", "name()", "p:r", nil, true},
			{"<p:r>8</p:r>", "namespace-uri()", "", nil, false},
			{"<r xmlns:xml='urn:wrong' xml:lang='en'>8</r>", ".", "8", float64(8), true},
			{"<r xmlns:xml='urn:wrong' xml:lang='en'>8</r>", "@xml:lang", "en", nil, true},
			{"<r xmlns:p='urn:x' xmlns:q='urn:x' p:a='1' q:a='2'/>", "count(@*)", "2", float64(2), true},
			{"<r xml:id='bad id'>8</r>", ".", "8", float64(8), true},
			{"<r xml:id='bad id'>8</r>", "@xml:id", "bad id", nil, true},
			{"<r xml:space='invalid'>8</r>", ".", "8", float64(8), true},
			{"<?xml version='1.2'?><r>8</r>", ".", "8", float64(8), true},
			{"<!DOCTYPE r [<!ATTLIST r a CDATA 'd'>]><r/>", "@a", "", nil, false},
			{"<!DOCTYPE r [<!ATTLIST r a CDATA 'd'>]><r/>", "count(@a)", "0", float64(0), false},
			{"<!DOCTYPE r [<!ATTLIST r a CDATA #REQUIRED>]><r/>", ".", "", nil, true},
			{"<r xmlns='urn:r'><a xmlns=''>8</a></r>", "namespace-uri(/*/a)", "", nil, false},
		} {
			xml2Both(t, p, b, c, xml2Scalars(tc.doc, tc.path, tc.s, tc.n, tc.b))
		}
		for _, doc := range []string{
			"<p:r>8</p:r>",
			"<r xmlns:xml='urn:wrong' xml:lang='en'>8</r>",
			"<r xmlns:p='urn:x' xmlns:q='urn:x' p:a='1' q:a='2'/>",
			"<r xml:id='bad id'>8</r>",
			"<?xml version='1.2'?><r>8</r>",
		} {
			xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid($1::text) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, doc))
		}
		xml2LegacyEncoding(t, p, b, c)
		xml2LegacyEvents(t, p, b, c)
		for _, system := range []string{"file:///stuntgres-xml2-nonexistent-entity", "http://127.0.0.1:1/stuntgres-xml2-nonexistent-entity"} {
			doc := "<!DOCTYPE r [<!ENTITY e SYSTEM '" + system + "'>]><r>A&e;B</r>"
			xml2Both(t, p, b, c, xml2Scalars(doc, ".", "AB", nil, true))
			xml2Both(t, p, b, c, xml2Scalars(doc, "count(node())", "1", float64(1), true))
			for _, fn := range []string{"xpath_string", "xpath_number", "xpath_bool"} {
				xml2Failure(t, p, b, c, "SELECT "+fn+"($1::text,$2::text)", xml2Compile("Invalid expression"), false, doc, "[")
			}
		}
		// A namespace declaration is not an automatic XPath prefix binding.
		xml2Both(t, p, b, c, fixture.Select("SELECT xpath_exists('/p:r', '<p:r xmlns:p=\"urn:r\"/>'::xml, ARRAY[ARRAY['p','urn:r']]) AS present",
			[]string{"present"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
