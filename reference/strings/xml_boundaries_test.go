package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func xmlBoundaryAcceptance(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Literal full diagnostics are independently observed on pinned PostgreSQL.
		for _, tc := range []struct{ input, detail string }{
			{"", "line 1: switching encoding : no input\n\n^\nline 1: Document is empty\n\n^"},
			{" ", "line 1: Start tag expected, '<' not found\n \n ^"},
			{"text<a/>", xmlFragmentDetail},
			{"<a/><b/>", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^"},
		} {
			d := xmlError("2200M", "invalid XML document", tc.detail, 0)
			xmlFailure(t, p, b, c, "SELECT XMLPARSE(DOCUMENT $1)", d, false, tc.input)
		}
		duplicate := xmlError("2200N", "invalid XML content", "line 1: Attribute x redefined\n<a x=\"1\" x=\"2\"/>\n              ^", 0)
		xmlFailure(t, p, b, c, "SELECT XMLPARSE(CONTENT $1)", duplicate, false, `<a x="1" x="2"/>`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN xml_nullable_domain AS xml", 0))
		for _, mode := range []string{"CONTENT", "DOCUMENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION "+mode, 0))
			for _, arg := range []any{nil, []byte(nil)} {
				s := xmlSelect("SELECT $1::xml_nullable_domain AS v", "XML", nil)
				s.Args = []any{arg}
				xmlBoth(t, p, b, c, s)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml_required(id int,v xml NOT NULL)", 0))
		d := xmlError("23502", `null value in column "v" of relation "xml_required" violates not-null constraint`, "Failing row contains (1, null).", 0)
		d.Schema, d.Table, d.Column = "public", "xml_required", "v"
		for _, arg := range []any{nil, []byte(nil)} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO xml_required VALUES(1,$1)", d, arg)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		xmlBoth(t, p, b, c, xmlSelect("SELECT count(*) AS v FROM xml_required", "INT8", int64(0)))
		if b.PG {
			return
		} // Native pre-cancel has an ordinary context-error contract.
		q := "INSERT INTO xml_required VALUES(2,XMLPARSE(DOCUMENT $1))"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := c.ExecContext(ctx, q, "<a/>"); !errors.Is(err, context.Canceled) {
			t.Fatalf("direct cancel %v", err)
		}
		if _, err := st.ExecContext(ctx, "<a/>"); !errors.Is(err, context.Canceled) {
			t.Fatalf("held cancel %v", err)
		}
		xmlBoth(t, p, b, c, xmlSelect("SELECT count(*) AS v FROM xml_required", "INT8", int64(0)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 1, "<healthy/>"))
		xmlBoth(t, p, b, c, xmlSelect("SELECT v AS v FROM xml_required", "XML", "<healthy/>"))
	})
}
