package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLInput(t *testing.T) {
	t.Run("boundaries", xmlBoundaryAcceptance)
	t.Run("catalog-source", xmlParseCatalogSource)
	t.Run("values", func(t *testing.T) {
		xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				expr  string
				value any
			}{
				{"XML '<a/>'", "<a/>"}, {"'<a/><b/>'::xml", "<a/><b/>"}, {"''::xml", ""}, {"NULL::xml", nil},
				{"XMLPARSE(DOCUMENT '<a>  <b/>  </a>')", "<a>  <b/>  </a>"},
				{"XMLPARSE(CONTENT 'text<a/>tail<b/>')", "text<a/>tail<b/>"},
				{"XMLPARSE(CONTENT 42)", "42"}, {"XMLPARSE(CONTENT true)", "true"},
				{"XMLPARSE(CONTENT ARRAY[1,2])", "{1,2}"}, {"XMLPARSE(CONTENT NULL::integer)", nil},
				{"XMLPARSE(CONTENT '<a/>'::xml)", "<a/>"}, {"XMLPARSE(CONTENT '<a/>'::varchar(9))", "<a/>"},
				{"XMLPARSE(CONTENT '<a/>'::character(9))", "<a/>"},
			} {
				s := xmlSelect("SELECT "+tc.expr+" AS v", "XML", tc.value)
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { xmlMetadata(t, r, b.PG, false) }
				xmlBoth(t, p, b, c, s)
			}
			// NAME uses generic typed XML output, unlike the released raw TEXT
			// cast and XMLSERIALIZE's ordinary NAME target conversion.
			declaration := `<?xml version="1.0" encoding="UTF8"?><a/>`
			casts := "SELECT XML '" + declaration + "'::text AS raw, XML '" + declaration + "'::name AS via_name, XMLSERIALIZE(CONTENT XML '" + declaration + "' AS name) AS serialized_name"
			xmlBoth(t, p, b, c, fixture.Select(casts, []string{"raw", "via_name", "serialized_name"}, []string{"TEXT", "NAME", "NAME"}, [][]any{{declaration, "<a/>", declaration}}))
			for _, mode := range []string{"DOCUMENT", "CONTENT"} {
				for _, ws := range []string{"PRESERVE", "STRIP"} {
					xmlBoth(t, p, b, c, xmlSelect("SELECT XMLPARSE("+mode+" ' <a>  <b/>  </a> ' "+ws+" WHITESPACE) AS v", "XML", " <a>  <b/>  </a> "))
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml_input(id int,v xml)", 0))
			for _, prepared := range []bool{false, true} {
				for i, arg := range []any{"<a/>", []byte("<b/>"), nil, []byte(nil), ""} {
					s := fixture.Exec("INSERT INTO xml_input VALUES($1,$2)", 1, i, arg)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			want := xmlSelect("SELECT v FROM xml_input ORDER BY id", "XML", "<a/>", "<a/>", "<b/>", "<b/>", nil, nil, nil, nil, "", "")
			want.CheckMetadata = func(t *testing.T, r *sql.Rows) { xmlMetadata(t, r, b.PG, true) }
			xmlBoth(t, p, b, c, want)
			empty := xmlSelect("SELECT v FROM xml_input WHERE id=-1", "XML")
			empty.CheckMetadata = want.CheckMetadata
			xmlBoth(t, p, b, c, empty)
			buffer := []byte("<owned/>")
			stmt, err := c.PrepareContext(p.Ctx, "SELECT $1::xml AS v,$1::xml AS again")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			r, err := stmt.QueryContext(p.Ctx, buffer)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			copy(buffer, []byte("<wrong/>"))
			if !r.Next() {
				t.Fatal("missing owned row", r.Err())
			}
			var a, z any
			if err := r.Scan(&a, &z); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, b.PG, "XML", a)
			fixture.AssertRawValue(t, b.PG, "XML", z)
			if fixture.Normalize(a) != "<owned/>" || fixture.Normalize(z) != "<owned/>" {
				t.Fatalf("borrowed parameter changed: %v %v", a, z)
			}
			if r.Next() || r.Err() != nil {
				t.Fatal("unexpected rows", r.Err())
			}
		})
	})
	t.Run("document-input", func(t *testing.T) {
		xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION DOCUMENT", 0))
			for _, arg := range []any{nil, []byte(nil)} {
				s := xmlSelect("SELECT $1::xml AS v", "XML", nil)
				s.Args = []any{arg}
				xmlBoth(t, p, b, c, s)
			}
			for _, tc := range []struct{ input, detail string }{
				{"", "line 1: switching encoding : no input\n\n^\nline 1: Document is empty\n\n^"},
				{" ", "line 1: Start tag expected, '<' not found\n \n ^"},
				{"text<a/>", xmlFragmentDetail},
				{"<a/><b/>", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^"},
			} {
				d := xmlError("2200M", "invalid XML document", tc.detail, 0)
				d.Context = "unnamed portal parameter $1 = '...'"
				if tc.input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				xmlFailure(t, p, b, c, "SELECT $1::xml", d, false, tc.input)
			}
			xmlBoth(t, p, b, c, xmlSelect("SELECT XMLPARSE(CONTENT '') AS v", "XML", ""))
			xmlBoth(t, p, b, c, xmlSelect("SELECT '<a/>'::xml AS v", "XML", "<a/>"))
		})
	})
}

func xmlParseCatalogSource(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		functionCreated, castCreated := false, false
		defer func() {
			if castCreated {
				if _, e := c.ExecContext(p.Ctx, "DROP CAST (integer AS text)"); e != nil {
					t.Error(e)
				}
			}
			if functionCreated {
				if _, e := c.ExecContext(p.Ctx, "DROP FUNCTION public.xml_source(integer)"); e != nil {
					t.Error(e)
				}
			}
		}()
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE FUNCTION public.xml_source(integer) RETURNS text LANGUAGE sql IMMUTABLE AS 'SELECT ''<custom/>'''", 0))
		functionCreated = true
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE CAST (integer AS text) WITH FUNCTION public.xml_source(integer)", 0))
		castCreated = true
		// An explicit-only catalog entry is not absent: assignment-context
		// XML source binding must reject it rather than fall back to text I/O.
		xmlFailure(t, p, b, c, "SELECT XMLPARSE(CONTENT 42), XMLPARSE(CONTENT NULL::integer)", xmlError("42804", "argument of XMLPARSE must be type text, not type integer", "", 25), true)
		if len(notices) != 0 {
			t.Fatalf("explicit XML source rejection emitted notices %#v", notices)
		}
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("DROP CAST (integer AS text)", 0))
		castCreated = false
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE CAST (integer AS text) WITH FUNCTION public.xml_source(integer) AS ASSIGNMENT", 0))
		castCreated = true
		// This nonSTRICT source conversion runs before XMLPARSE null handling.
		q := "SELECT XMLPARSE(DOCUMENT 42), XMLPARSE(DOCUMENT NULL::integer)"
		xmlBoth(t, p, b, c, fixture.Select(q, []string{"xmlparse", "xmlparse"}, []string{"XML", "XML"}, [][]any{{"<custom/>", "<custom/>"}}))
		if len(notices) != 0 {
			t.Fatalf("assignment XML source emitted notices %#v", notices)
		}
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("DROP CAST (integer AS text)", 0))
		castCreated = false
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("DROP FUNCTION public.xml_source(integer)", 0))
		functionCreated = false
		if len(notices) != 0 {
			t.Fatalf("XML source cleanup emitted notices %#v", notices)
		}
	})
}
