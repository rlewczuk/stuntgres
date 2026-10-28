package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLSessionPrepared(t *testing.T) {
	xmlSessionPrepared(t)
	t.Run("cursor-execution-mode", xmlCursorExecutionMode)
}

func xmlSessionPrepared(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		show := func(conn *sql.Conn, mode string) {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SHOW xmloption", []string{"xmloption"}, []string{"TEXT"}, [][]any{{mode}}))
		}
		exec := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		show(c, "content")
		show(peer, "content")
		for _, q := range []string{"SET xmloption='document'", "SET XML OPTION DOCUMENT"} {
			exec(q)
			show(c, "document")
			show(peer, "content")
		}
		for _, q := range []string{"SET xmloption=DEFAULT", "RESET xmloption", "RESET ALL"} {
			exec("SET XML OPTION DOCUMENT")
			exec(q)
			show(c, "content")
		}
		exec("BEGIN")
		exec("SET LOCAL xmloption='document'")
		show(c, "document")
		exec("SAVEPOINT xml_mode")
		exec("SET LOCAL xmloption='content'")
		show(c, "content")
		exec("ROLLBACK TO xml_mode")
		show(c, "document")
		exec("COMMIT")
		show(c, "content")
		exec("BEGIN")
		exec("SET XML OPTION DOCUMENT")
		exec("ROLLBACK")
		show(c, "content")
		invalid := xmlError("22023", `invalid value for parameter "xmloption": "bogus"`, "", 0)
		invalid.Hint = "Available values: content, document."
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SET xmloption='bogus'", invalid))
		show(c, "content")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SET XML OPTION bogus", xmlError("42601", `syntax error at or near "bogus"`, "", 16)))
		cases := []struct {
			q       string
			args    []any
			current bool
			portal  bool
		}{
			{"SELECT XML 'text<a/>' AS v", nil, false, false},
			{"SELECT 'text<a/>'::xml AS v", nil, false, false},
			{"SELECT $1::xml AS v", []any{"text<a/>"}, true, true},
			{"SELECT $1::text::xml AS v", []any{"text<a/>"}, true, false},
			{"SELECT 'text<a/>'::text::xml AS v", nil, true, false},
			{"SELECT XMLPARSE(CONTENT 'text<a/>') AS v", nil, false, false},
		}
		statements := make([]*sql.Stmt, len(cases))
		for i, tc := range cases {
			st, e := c.PrepareContext(p.Ctx, tc.q)
			if e != nil {
				t.Fatal(e)
			}
			statements[i] = st
			defer st.Close()
		}
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			exec("SET XML OPTION " + mode)
			for i, tc := range cases {
				runner := fixture.HeldStatement{Executor: c, Statement: statements[i], SQL: tc.q}
				if mode == "DOCUMENT" && tc.current {
					d := xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0)
					if tc.portal {
						d.Context = "unnamed portal parameter $1 = '...'"
					}
					s := fixture.ErrorDiagnostic(tc.q, d, tc.args...)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, runner, b.Recorder, s)
				} else {
					s := xmlSelect(tc.q, "XML", "text<a/>")
					s.Args = tc.args
					fixture.RunStep(t, p.Ctx, runner, b.Recorder, s)
				}
			}
			show(peer, "content")
		}
		xmlFailure(t, p, b, c, "SELECT XMLPARSE(CONTENT '<a>')", xmlError("2200N", "invalid XML content", xmlBadA, 0), false)
		xmlFailure(t, p, b, c, "SELECT XML '<a>'", xmlError("2200N", "invalid XML content", xmlBadA, 12), true)
		exec("SET XML OPTION DOCUMENT")
		xmlFailure(t, p, b, c, "SELECT XML 'text<a/>'", xmlError("2200M", "invalid XML document", xmlFragmentDetail, 12), true)
		// Inferred XML parameter uses the same full portal provenance as explicit XML.
		exec("SET XML OPTION CONTENT")
		exec("CREATE TABLE xml_inferred(v xml)")
		st, e := c.PrepareContext(p.Ctx, "INSERT INTO xml_inferred VALUES($1)")
		if e != nil {
			t.Fatal(e)
		}
		defer st.Close()
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			exec("SET XML OPTION " + mode)
			s := fixture.Exec("INSERT INTO xml_inferred VALUES($1)", 1, "text<a/>")
			if mode == "DOCUMENT" {
				d := xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0)
				d.Context = "unnamed portal parameter $1 = '...'"
				s = fixture.ErrorDiagnostic(s.Query, d, "text<a/>")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: s.Query}, b.Recorder, s)
		}
		xmlBoth(t, p, b, c, xmlSelect("SELECT v FROM xml_inferred", "XML", "text<a/>", "text<a/>", "text<a/>", "text<a/>"))
	})
}

func xmlCursorExecutionMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		typed, between bool
	}{
		{"text-before-first-fetch", false, false}, {"text-between-fetches", false, true},
		{"literal-before-first-fetch", true, false}, {"literal-between-fetches", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				exec := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
				exec("SET XML OPTION CONTENT")
				exec("BEGIN")
				rolledBack := false
				defer func() {
					if !rolledBack {
						if _, e := c.ExecContext(p.Ctx, "ROLLBACK"); e != nil {
							t.Error(e)
						}
					}
				}()
				source := "SELECT id, v::xml AS x FROM (VALUES (1,'text<a/>'::text),(2,'text<a/>'::text)) AS source(id,v)"
				if tc.typed {
					source = "SELECT id, XML 'text<a/>' AS x FROM (VALUES (1),(2)) AS source(id)"
				}
				exec("DECLARE xml_cursor NO SCROLL CURSOR FOR " + source)
				fetch := func(id int64) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("FETCH FORWARD 1 FROM xml_cursor", []string{"id", "x"}, []string{"INT4", "XML"}, [][]any{{id, "text<a/>"}}))
				}
				if tc.between {
					fetch(1)
				}
				exec("SET XML OPTION DOCUMENT")
				if tc.typed {
					id := int64(1)
					if tc.between {
						id = 2
					}
					fetch(id)
					exec("SET XML OPTION CONTENT")
					if !tc.between {
						fetch(2)
					} else {
						exec("FETCH FORWARD 1 FROM xml_cursor")
					}
				} else {
					s := fixture.ErrorDiagnostic("FETCH FORWARD 1 FROM xml_cursor", xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0))
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", xmlError("25P02", "current transaction is aborted, commands ignored until end of transaction block", "", 0)))
				}
				exec("ROLLBACK")
				rolledBack = true
				xmlBoth(t, p, b, c, xmlSelect("SELECT '<healthy/>'::xml AS v", "XML", "<healthy/>"))
			})
		})
	}
}
