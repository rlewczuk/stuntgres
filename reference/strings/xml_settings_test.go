package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLSettingsBinaryLifecycle(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		inTransaction := false
		defer func() {
			if inTransaction {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
					t.Error(err)
				}
			}
		}()
		exec := func(q string) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			switch q {
			case "BEGIN":
				inTransaction = true
			case "COMMIT", "ROLLBACK":
				inTransaction = false
			}
		}
		witness := func(conn *sql.Conn, hex bool) {
			t.Helper()
			e, f := `<r b="q83v">q83v</r>`, `<b>q83v</b>`
			if hex {
				e, f = `<r b="ABCDEF">ABCDEF</r>`, `<b>ABCDEF</b>`
			}
			xmlBoth(t, p, b, conn, fixture.Select(
				"SELECT xmlelement(NAME r, XMLATTRIBUTES($1::bytea AS b), $1::bytea) AS e, xmlforest($1::bytea AS b) AS f",
				[]string{"e", "f"}, []string{"XML", "XML"}, [][]any{{e, f}}, []byte{0xab, 0xcd, 0xef},
			))
		}
		witness(c, false)
		witness(peer, false)
		exec("SET SESSION xmlbinary='hex'")
		witness(c, true)
		witness(peer, false)
		invalid := xmlError("22023", `invalid value for parameter "xmlbinary": "bogus"`, "", 0)
		invalid.Hint = "Available values: base64, hex."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SET xmlbinary='bogus'", invalid)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			witness(c, true)
		}
		witness(peer, false)

		exec("BEGIN")
		exec("SET LOCAL xmlbinary='base64'")
		witness(c, false)
		witness(peer, false)
		exec("SAVEPOINT binary_restore")
		exec("SET LOCAL xmlbinary='hex'")
		witness(c, true)
		exec("ROLLBACK TO binary_restore")
		witness(c, false)
		exec("COMMIT")
		witness(c, true)
		witness(peer, false)

		exec("BEGIN")
		exec("SET xmlbinary='base64'")
		witness(c, false)
		exec("ROLLBACK")
		witness(c, true)
		exec("BEGIN")
		exec("SET xmlbinary='base64'")
		witness(c, false)
		exec("COMMIT")
		witness(c, false)
		for _, q := range []string{"SET xmlbinary=DEFAULT", "RESET xmlbinary", "RESET ALL"} {
			exec("SET xmlbinary='hex'")
			witness(c, true)
			exec(q)
			witness(c, false)
		}
		witness(peer, false)
		xmlBoth(t, p, b, c, xmlSelect("SELECT '<healthy/>'::xml AS v", "XML", "<healthy/>"))
	})
}

func TestReferenceXMLSettingsBinaryNullEmpty(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, mode := range []string{"base64", "hex"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET xmlbinary='"+mode+"'", 0))
			for _, tc := range []struct {
				expression string
				want       any
			}{
				{"xmlelement(NAME r,NULL::bytea)", "<r/>"},
				{"xmlelement(NAME r,XMLATTRIBUTES(NULL::bytea AS b))", "<r/>"},
				{"xmlforest(NULL::bytea AS b)", nil},
				{"xmlelement(NAME r,decode('','hex'))", "<r></r>"},
				{"xmlelement(NAME r,XMLATTRIBUTES(decode('','hex') AS b))", `<r b=""/>`},
				{"xmlforest(decode('','hex') AS b)", "<b></b>"},
			} {
				xmlBoth(t, p, b, c, xmlSelect("SELECT "+tc.expression+" AS v", "XML", tc.want))
			}
		}
	})
}

func TestReferenceXMLSettingsOptionLifecycle(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		inTransaction := false
		defer func() {
			if inTransaction {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
					t.Error(err)
				}
			}
		}()
		exec := func(q string) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			switch q {
			case "BEGIN":
				inTransaction = true
			case "COMMIT", "ROLLBACK":
				inTransaction = false
			}
		}
		const query = "SELECT $1::text::xml AS v"
		documentError := xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0)
		witness := func(conn *sql.Conn, document bool) {
			t.Helper()
			if !document {
				s := xmlSelect(query, "XML", "text<a/>")
				s.Args = []any{"text<a/>"}
				xmlBoth(t, p, b, conn, s)
				return
			}
			if !inTransaction {
				xmlFailure(t, p, b, conn, query, documentError, false, "text<a/>")
				return
			}
			// Each failing route gets its own probe after the setting transition.
			// Recovery therefore preserves the effective DOCUMENT setting and the
			// enclosing lifecycle savepoint, rather than masking it with 25P02.
			for _, prepared := range []bool{false, true} {
				exec("SAVEPOINT option_probe")
				s := fixture.ErrorDiagnostic(query, documentError, "text<a/>")
				s.SelectRows, s.ErrorStage = true, "execute"
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				exec("ROLLBACK TO option_probe")
				exec("RELEASE SAVEPOINT option_probe")
			}
		}
		witness(c, false)
		witness(peer, false)
		exec("SET SESSION xmloption='document'")
		witness(c, true)
		witness(peer, false)
		exec("RESET xmloption")
		witness(c, false)

		exec("BEGIN")
		exec("SET LOCAL xmloption='document'")
		witness(c, true)
		witness(peer, false)
		exec("SAVEPOINT option_restore")
		exec("SET LOCAL xmloption='content'")
		witness(c, false)
		exec("ROLLBACK TO option_restore")
		witness(c, true)
		exec("COMMIT")
		witness(c, false)
		witness(peer, false)

		exec("BEGIN")
		exec("SET xmloption='document'")
		witness(c, true)
		exec("ROLLBACK")
		witness(c, false)
		exec("BEGIN")
		exec("SET xmloption='document'")
		witness(c, true)
		exec("COMMIT")
		witness(c, true)
		exec("RESET xmloption")
		witness(c, false)
		for _, q := range []string{"SET xmloption=DEFAULT", "RESET xmloption", "RESET ALL"} {
			exec("SET xmloption='document'")
			witness(c, true)
			exec(q)
			witness(c, false)
		}
		invalid := xmlError("22023", `invalid value for parameter "xmloption": "bogus"`, "", 0)
		invalid.Hint = "Available values: content, document."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SET xmloption='bogus'", invalid)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			witness(c, false)
		}
		witness(peer, false)
		xmlBoth(t, p, b, c, xmlSelect("SELECT '<healthy/>'::xml AS v", "XML", "<healthy/>"))
	})
}

func TestReferenceXMLSettingsExplicitModes(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, mode := range []string{"content", "document"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET xmloption='"+mode+"'", 0))
			for _, tc := range []struct {
				query string
				typ   string
			}{
				{"SELECT XMLPARSE(CONTENT $1::text) AS v", "XML"},
				{"SELECT XMLSERIALIZE(CONTENT XMLPARSE(CONTENT $1::text) AS text NO INDENT) AS v", "TEXT"},
				{"SELECT XMLPARSE(CONTENT $1::text)::text AS v", "TEXT"},
				{"SELECT XMLPARSE(CONTENT $1::text)::varchar AS v", "VARCHAR"},
				{"SELECT XMLPARSE(CONTENT $1::text)::bpchar AS v", "BPCHAR"},
			} {
				s := xmlSelect(tc.query, tc.typ, "text<a/>")
				s.Args = []any{"text<a/>"}
				xmlBoth(t, p, b, c, s)
			}
			xmlFailure(t, p, b, c, "SELECT XMLPARSE(DOCUMENT $1::text) AS v",
				xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0), false, "text<a/>")
			xmlFailure(t, p, b, c, "SELECT XMLSERIALIZE(DOCUMENT XMLPARSE(CONTENT $1::text) AS text) AS v",
				xmlError("2200L", "not an XML document", "", 0), false, "text<a/>")
		}
	})
}
