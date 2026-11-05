package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = recorder
			xml2Install(t, p, b, c)
			xtSeed(t, p, b, c)
			if len(notices) != 0 {
				t.Fatalf("unexpected setup notices=%#v", notices)
			}
			number := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "unsupported XQuery result: 3"}
			boolean := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "unsupported XQuery result: 2"}
			for _, tc := range []struct {
				name, path, definitions string
				columns, types          []string
				rows                    [][]any
				wantNotices             []fixture.Diagnostic
			}{
				{"unsupported_driven", "/doc/a|count(/doc/a)|boolean(/doc/a)", "id integer,a text,n text,b text", []string{"id", "a", "n", "b"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "10", "<unsupported/>", "<unsupported/>"}, {int64(1), "20", "<unsupported/>", "<unsupported/>"}}, []fixture.Diagnostic{number, boolean, number, boolean, number, boolean}},
				{"number_scalar_only", "count(/doc/a)", "id integer,n text", []string{"id", "n"}, []string{"INT4", "TEXT"}, nil, []fixture.Diagnostic{number}},
				{"boolean_scalar_only", "boolean(/doc/a)", "id integer,b text", []string{"id", "b"}, []string{"INT4", "TEXT"}, nil, []fixture.Diagnostic{boolean}},
				{"undefined_prefix_evaluation", "/doc/a|/p:doc/a", "id integer,a text,b text", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "10", nil}, {int64(1), "20", nil}}, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, route := range []string{"direct", "prepared"} {
						t.Run(route, func(t *testing.T) {
							notices = nil
							defer func() {
								if !reflect.DeepEqual(notices, tc.wantNotices) {
									t.Errorf("notices=%#v want=%#v", notices, tc.wantNotices)
								}
								notices = nil
							}()
							s := fixture.Select(xtQuery("id", "doc", "xt_src", tc.path, "id=1", tc.definitions), tc.columns, tc.types, tc.rows)
							s.Bag, s.Prepared = true, route == "prepared"
							fixture.RunStep(t, p.Ctx, c, recorder, s)
						})
					}
				})
			}
			xtRawMemory(t, p, b, c, &notices)
		})
	}
}

func xtRawMemory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
	t.Helper()
	q := "SELECT * FROM xpath_table($1::text,$2::text,$3::text,$4::text,$5::text) AS x(id integer,a text,b text)"
	// The shared character-input fixtures require this complete parameter
	// diagnostic on both adapters, before any inner XML parsing.
	invalidText := xml2Error("22021", `invalid byte sequence for encoding "UTF8": 0xff`)
	invalidText.Context = "unnamed portal parameter $2"
	for _, tc := range []struct {
		name, doc, path string
		value           any
		wantError       fixture.Diagnostic
	}{
		{"declared_prefix_not_registered", `<p:doc xmlns:p="urn:p"><a>8</a></p:doc>`, "/*/a|/p:doc/a", "8", fixture.Diagnostic{}},
		{"legacy_undefined_prefix", "<p:r>8</p:r>", "/*", "8", fixture.Diagnostic{}},
		{"legacy_wrong_xml_binding", "<r xmlns:xml='urn:wrong' xml:lang='en'>8</r>", "/*", "8", fixture.Diagnostic{}},
		{"legacy_namespace_compile", "<p:r>8</p:r>", "[", nil, xml2Compile("line 1: Namespace prefix p on r is not defined\n<p:r>8</p:r>\n    ^\nInvalid expression")},
		{"legacy_version_compile", "<?xml version='1.2'?><r>8</r>", "[", nil, xml2Compile("line 1: Unsupported version '1.2'\n<?xml version='1.2'?><r>8</r>\n                   ^\nInvalid expression")},
		{"latin1_raw_memory", `<?xml version="1.0" encoding="ISO-8859-1"?><doc><a>é</a></doc>`, "/doc/a", "Ã©", fixture.Diagnostic{}},
		{"latin1_reference", `<?xml version="1.0" encoding="ISO-8859-1"?><doc><a>&#233;</a></doc>`, "/doc/a", "é", fixture.Diagnostic{}},
		{"raw_shift_malformed", "<?xml version=\"1.0\" encoding=\"ISO-2022-JP\"?><doc><a>\x1b$X</a></doc>", "[", nil, fixture.Diagnostic{}},
		{"raw_utf16_mismatch", `<?xml version="1.0" encoding="UTF-16"?><doc><a>é</a></doc>`, "[", nil, fixture.Diagnostic{}},
		{"raw_unknown_encoding", `<?xml version="1.0" encoding="NO-SUCH-ENCODING"?><doc><a>é</a></doc>`, "[", nil, fixture.Diagnostic{}},
		{"malformed_text_parameter_bytes", "<doc><a>\xff</a></doc>", "/doc/a", nil, invalidText},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*notices = nil
			defer func() {
				if len(*notices) != 0 {
					t.Errorf("unexpected raw-memory notices=%#v", *notices)
				}
				*notices = nil
			}()
			// The document fragment is SQL TEXT inside SPI, not an XML cast.
			args := []any{"id", xtQuote(tc.doc), "xt_src", tc.path, "id=1"}
			s := fixture.Select(q, []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), tc.value, nil}}, args...)
			if tc.wantError.Code != "" {
				s = fixture.ErrorDiagnostic(q, tc.wantError, args...)
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			xtBoth(t, p, b, c, "raw_document", s)
		})
	}
}
