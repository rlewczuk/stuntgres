package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLDiagnostics(t *testing.T) {
	t.Run("errors", func(t *testing.T) {
		xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				q, code, message, detail string
				pos                      int
				analysis                 bool
			}{
				{"SELECT '<a>'::xml", "2200N", "invalid XML content", xmlBadA, 8, true},
				{"SELECT XML '<a>'", "2200N", "invalid XML content", xmlBadA, 12, true},
				{"SELECT\n  'é' AS marker,\n  XML '<a>'", "2200N", "invalid XML content", xmlBadA, 31, true},
				{"SELECT XMLPARSE(CONTENT '<a>')", "2200N", "invalid XML content", xmlBadA, 0, false},
				{"SELECT XMLPARSE(DOCUMENT '<a>')", "2200M", "invalid XML document", xmlBadA, 0, false},
				{"SELECT XMLPARSE(DOCUMENT 42)", "2200M", "invalid XML document", "line 1: Start tag expected, '<' not found\n42\n^", 0, false},
				{"SELECT XMLPARSE(CONTENT '<a/>' NO PRESERVE WHITESPACE)", "42601", `syntax error at or near "NO"`, "", 32, true},
				{"SELECT XMLPARSE(CONTENT '" + xmlAdjacency + "')", "2200N", "invalid XML content: invalid XML declaration", "Space required.", 0, false},
			} {
				xmlFailure(t, p, b, c, tc.q, xmlError(tc.code, tc.message, tc.detail, tc.pos), tc.analysis)
			}
			multiline := "line 2: Opening and ending tag mismatch: b line 2 and a\n<b></a>\n       ^\nline 2: Premature end of data in tag a line 1\n<b></a>\n       ^"
			for _, mode := range []string{"CONTENT", "DOCUMENT"} {
				code, msg := "2200N", "invalid XML content"
				if mode == "DOCUMENT" {
					code, msg = "2200M", "invalid XML document"
				}
				xmlFailure(t, p, b, c, "SELECT XMLPARSE("+mode+" E'<a>\\n<b></a>')", xmlError(code, msg, multiline, 0), false)
			}
			for _, q := range []string{"SELECT $1::xml", "SELECT $1::text::xml", "SELECT XMLPARSE(CONTENT $1)", "SELECT XMLPARSE(DOCUMENT $1)"} {
				d := xmlError("2200N", "invalid XML content", xmlBadA, 0)
				if q == "SELECT $1::xml" {
					d.Context = "unnamed portal parameter $1 = '...'"
				}
				if q == "SELECT XMLPARSE(DOCUMENT $1)" {
					d.Code, d.Message = "2200M", "invalid XML document"
				}
				xmlFailure(t, p, b, c, q, d, false, "<a>")
			}
			for _, tc := range []struct {
				q, message, hint string
				pos              int
			}{
				{"SELECT '<a/>'::xml = '<a/>'::xml", "operator does not exist: xml = xml", "No operator matches the given name and argument types. You might need to add explicit type casts.", 20},
				{"SELECT '<a/>'::xml < '<b/>'::xml", "operator does not exist: xml < xml", "No operator matches the given name and argument types. You might need to add explicit type casts.", 20},
				{"SELECT '<a/>'::xml AS v ORDER BY v", "could not identify an ordering operator for type xml", "Use an explicit ordering operator or modify the query.", 34},
				{"SELECT '<a/>'::xml AS v GROUP BY v", "could not identify an equality operator for type xml", "", 34},
				{"SELECT DISTINCT '<a/>'::xml", "could not identify an equality operator for type xml", "", 17},
				{"SELECT '<a/>'::xml UNION SELECT '<b/>'::xml", "could not identify an equality operator for type xml", "", 8},
			} {
				d := xmlError("42883", tc.message, "", tc.pos)
				d.Hint = tc.hint
				xmlFailure(t, p, b, c, tc.q, d, true)
			}
			xmlBoth(t, p, b, c, xmlSelect("SELECT '<a/>'::xml::text = '<a/>'::xml::text AS v", "BOOL", true))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml_oracle(id int,v xml)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml_oracle VALUES (1,'<a/>'),(2,NULL)", 2))
			for _, method := range []string{"btree", "hash"} {
				d := xmlError("42704", `data type xml has no default operator class for access method "`+method+`"`, "", 0)
				d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("CREATE INDEX xml_oracle_"+method+" ON xml_oracle USING "+method+"(v)", d))
			}
			xmlBoth(t, p, b, c, xmlSelect("SELECT count(*) AS v FROM pg_class WHERE relname IN ('xml_oracle_btree','xml_oracle_hash')", "INT8", int64(0)))
			d := xmlError("2200N", "invalid XML content", "line 1: Premature end of data in tag bad line 1\n<bad>\n     ^", 46)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO xml_oracle VALUES (3,'<ok/>'),(4,'<bad>')", d))
			want := fixture.Select("SELECT id,v FROM xml_oracle ORDER BY id", []string{"id", "v"}, []string{"INT4", "XML"}, [][]any{{int64(1), "<a/>"}, {int64(2), nil}})
			xmlBoth(t, p, b, c, want)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml_oracle VALUES (3,'<c/>')", 1))
			d.Position = "34"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO xml_oracle VALUES (4,'<bad>')", d))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", xmlError("25P02", "current transaction is aborted, commands ignored until end of transaction block", "", 0)))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
			xmlBoth(t, p, b, c, want)
		})
	})
	t.Run("notices", xmlNotices)
}

func xmlNotices(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		warning := func(code, message, detail string, pos int) fixture.Diagnostic {
			d := xmlError(code, message, detail, pos)
			d.Severity, d.SeverityUnlocalized = "WARNING", "WARNING"
			return d
		}
		space := warning("01000", "line 1: Invalid value \"bad\" for xml:space : \"default\" or \"preserve\" expected\n<a xml:space=\"bad\"/>\n                  ^", "", 0)
		version := warning("01000", "line 1: Unsupported version '1.1'\n<?xml version=\"1.1\"?><a/>\n                   ^", "", 0)
		output := warning("XX001", "could not parse XML declaration in stored value", "Space required.", 0)
		check := func(want ...fixture.Diagnostic) {
			t.Helper()
			if len(notices) != len(want) || len(want) > 0 && !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices got %#v want %#v", notices, want)
			}
			notices = nil
		}
		declaration := `<?xml version="1.0" encoding="UTF8"?><a/>`
		casts := "SELECT XML '" + declaration + "'::text AS raw, XML '" + declaration + "'::name AS via_name, XMLSERIALIZE(CONTENT XML '" + declaration + "' AS name) AS serialized_name"
		// Undemanded execution-time XML remains unevaluated even beneath a
		// character cast, an ordinary function, or an unchosen CASE arm.
		// Demanded XML emits its actual parser/serializer warnings once per row.
		for _, tc := range []struct {
			s    fixture.Step
			want []fixture.Diagnostic
		}{
			{xmlSelect("SELECT XMLPARSE(CONTENT '<a>')::text AS v WHERE false", "TEXT"), nil},
			{xmlSelect("SELECT length(XMLSERIALIZE(CONTENT XMLPARSE(CONTENT '<a>') AS text)) AS v WHERE false", "INT4"), nil},
			{fixture.Select("SELECT CASE WHEN false THEN XMLPARSE(CONTENT '<a>')::text ELSE 'ok' END", []string{"case"}, []string{"TEXT"}, [][]any{{"ok"}}), nil},
			{xmlSelect(`SELECT XMLPARSE(DOCUMENT '<a xml:space="bad"/>')::text AS v FROM (VALUES(1),(2))s(id)`, "TEXT", `<a xml:space="bad"/>`, `<a xml:space="bad"/>`), []fixture.Diagnostic{space, space}},
			{xmlSelect(`SELECT length(XMLSERIALIZE(DOCUMENT XMLPARSE(CONTENT '<?xml version="1.1"?><a/>') AS text INDENT)) AS v FROM(VALUES(1),(2))s(id)`, "INT4", int64(43), int64(43)), []fixture.Diagnostic{version, version}},
			{fixture.Select(casts, []string{"raw", "via_name", "serialized_name"}, []string{"TEXT", "NAME", "NAME"}, [][]any{{declaration, "<a/>", declaration}}), nil},
		} {
			fixture.RunStep(t, p.Ctx, c, rec, tc.s)
			check(tc.want...)
			stmt, e := c.PrepareContext(p.Ctx, tc.s.Query)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			check()
			for range 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.s.Query}, rec, tc.s)
				check(tc.want...)
			}
			if e := stmt.Close(); e != nil {
				t.Fatal(e)
			}
		}
		// Typed XML literals are accepted during analysis, unlike XMLPARSE:
		// an undemanded projection still cannot hide malformed literal input.
		xmlFailure(t, p, b, c, "SELECT XML '<a>'::text AS v WHERE false", xmlError("2200N", "invalid XML content", xmlBadA, 12), true)
		check()
		for _, tc := range []struct {
			q, value, typ string
			want          []fixture.Diagnostic
		}{
			{`SELECT XMLPARSE(DOCUMENT '<a xml:space="bad"/>') AS v`, `<a xml:space="bad"/>`, "XML", []fixture.Diagnostic{space}},
			{`SELECT XMLPARSE(DOCUMENT '<?xmlfoo?><a/>') AS v`, `<?xmlfoo?><a/>`, "XML", []fixture.Diagnostic{warning("01000", "line 1: xmlParsePITarget: invalid name prefix 'xml'\n<?xmlfoo?><a/>\n        ^", "", 0)}},
			{`SELECT XMLSERIALIZE(DOCUMENT XMLPARSE(CONTENT '<?xml version="1.1"?><a/>') AS text INDENT) AS v`, "<?xml version=\"1.1\" encoding=\"UTF-8\"?>\n<a/>", "TEXT", []fixture.Diagnostic{version}},
			{`SELECT XMLSERIALIZE(DOCUMENT XMLPARSE(DOCUMENT '<?xml version="1.1"?><a/>') AS text INDENT) AS v`, "<?xml version=\"1.1\" encoding=\"UTF-8\"?>\n<a/>", "TEXT", []fixture.Diagnostic{version, version}},
			{`SELECT XMLSERIALIZE(CONTENT XMLPARSE(CONTENT '<?xml version="1.1"?><a/>') AS text INDENT) AS v`, "<a/>", "TEXT", nil},
		} {
			stmt, e := c.PrepareContext(p.Ctx, tc.q)
			if e != nil {
				t.Fatal(e)
			}
			check()
			for range 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}, rec, xmlSelect(tc.q, tc.typ, tc.value))
				check(tc.want...)
			}
			stmt.Close()
		}
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("SET XML OPTION DOCUMENT", 0))
		check()
		q := `SELECT XML '<a xml:space="bad"/>' AS v`
		st, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer st.Close()
		positioned := space
		positioned.Position = "12"
		check(positioned)
		for range 2 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, rec, xmlSelect(q, "XML", `<a xml:space="bad"/>`))
			check()
		}
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE TABLE xml_output(v xml)", 0))
		check()
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("INSERT INTO xml_output SELECT XMLPARSE(DOCUMENT '"+xmlAdjacency+"')", 1))
		check()
		// Stored and runtime XMLPARSE NAME casts warn on every execution.
		// The captured literal NAME cast warns on its first held Execute only;
		// all Prepare calls are quiet. Raw TEXT and serialization AS NAME are quiet.
		for _, tc := range []struct {
			q, column, typ  string
			warn, firstOnly bool
		}{
			{"SELECT v::text AS raw FROM xml_output", "raw", "TEXT", false, false},
			{"SELECT v::name AS via_name FROM xml_output", "via_name", "NAME", true, false},
			{"SELECT XMLSERIALIZE(CONTENT v AS name) AS serialized_name FROM xml_output", "serialized_name", "NAME", false, false},
			{"SELECT XML '" + xmlAdjacency + "'::text AS raw_literal", "raw_literal", "TEXT", false, false},
			{"SELECT XML '" + xmlAdjacency + "'::name AS name_literal", "name_literal", "NAME", true, true},
			{"SELECT XMLSERIALIZE(CONTENT XML '" + xmlAdjacency + "' AS name) AS serialized_name_literal", "serialized_name_literal", "NAME", false, false},
			{"SELECT XMLPARSE(DOCUMENT '" + xmlAdjacency + "')::name AS via_name", "via_name", "NAME", true, false},
		} {
			s := fixture.Select(tc.q, []string{tc.column}, []string{tc.typ}, [][]any{{xmlAdjacency}})
			want := []fixture.Diagnostic(nil)
			if tc.warn {
				want = []fixture.Diagnostic{output}
			}
			fixture.RunStep(t, p.Ctx, c, rec, s)
			check(want...)
			stmt, e := c.PrepareContext(p.Ctx, tc.q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			check()
			for execution := range 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}, rec, s)
				if tc.firstOnly && execution > 0 {
					check()
				} else {
					check(want...)
				}
			}
			if e := stmt.Close(); e != nil {
				t.Fatal(e)
			}
		}
		for range 2 {
			fixture.RunStep(t, p.Ctx, c, rec, xmlSelect("SELECT v AS v FROM xml_output", "XML", xmlAdjacency))
			check(output)
		}
		for _, expr := range []string{"v::text", "XMLSERIALIZE(CONTENT v AS text NO INDENT)", "XMLSERIALIZE(DOCUMENT v AS text NO INDENT)"} {
			fixture.RunStep(t, p.Ctx, c, rec, xmlSelect("SELECT "+expr+" AS v FROM xml_output", "TEXT", xmlAdjacency))
			check()
		}
		fixture.RunStep(t, p.Ctx, c, rec, xmlSelect("SELECT XMLSERIALIZE(DOCUMENT v AS text INDENT) AS v FROM xml_output", "TEXT", "<a/>"))
		check()
		fixture.RunStep(t, p.Ctx, c, rec, xmlSelect("SELECT v AS v FROM xml_output WHERE false", "XML"))
		check()
		fixture.RunStep(t, p.Ctx, c, rec, xmlSelect("SELECT NULL::xml AS v", "XML", nil))
		check()
		// Two actual output fields require two warnings; metadata and EOF do not.
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Select("SELECT v,v AS again FROM xml_output", []string{"v", "again"}, []string{"XML", "XML"}, [][]any{{xmlAdjacency, xmlAdjacency}}))
		check(output, output)
	})
}
