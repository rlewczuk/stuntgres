package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2ASCIIJP2Diagnostics(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, fn := range []string{"xpath_string", "xpath_number", "xpath_bool"} {
			q := "SELECT " + fn + "($1::text,$2::text) AS value"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal("parameterized prepare must be quiet", err) }
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			typ, healthy := "TEXT", any("12")
			if fn == "xpath_number" { typ, healthy = "FLOAT4", float64(12) }
			if fn == "xpath_bool" { typ, healthy = "BOOL", true }
			for _, doc := range []string{
				xml2RawDocument("ASCII", "12"), xml2RawDocument("US-ASCII", "12"),
				xml2RawDocument("ISO-2022-JP-2", "\x1b$(D+1\x1b(B"),
				xml2RawDocument("ISO2022JP2", "\x1b$(CGQ\x1b(B"),
			} {
				xml2Failure(t, p, b, c, q, xml2Compile("Invalid expression"), false, doc, "[")
				step := fixture.ErrorDiagnostic(q, xml2Compile("Invalid expression"), doc, "[")
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, h, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"value"}, []string{typ}, [][]any{{healthy}}, xml2RawDocument("ISO-2022-JP-2", "12"), "."))
			}
			// These are document failures, not XPath compile errors or notices.
			for _, doc := range []string{
				xml2RawDocument("ASCII", "é"), xml2RawDocument("US-ASCII", "é"),
				xml2RawDocument("ISO-2022-JP-2", "\x1b$X"),
				xml2RawDocument("ISO-2022-JP-2", "\x1b$(D!!\x1b(B"),
				xml2RawDocument("ISO-2022-JP-2", "\x1bNi"),
			} {
				value := any(nil)
				if fn == "xpath_bool" { value = false }
				s := fixture.Select(q, []string{"value"}, []string{typ}, [][]any{{value}}, doc, "[")
				xml2Both(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			}
			if err := stmt.Close(); err != nil { t.Fatal(err) }
		}
		// Warning source windows precede the converter's replacement buffer.
		// Full Detail is independently source-derived and qualified against
		// the pinned PostgreSQL reader, not learned from native output.
		for _, label := range []string{"ASCII", "ISO-2022-JP-2"} {
			doc := strings.Replace(xml2RawDocument(label, "12"), `version="1.0"`, `version="1.2"`, 1)
			detail := "line 1: Unsupported version '1.2'\n" + doc[:min(len(doc), 80)] + "\n" + strings.Repeat(" ", 19) + "^\nInvalid expression"
			xml2Failure(t, p, b, c, "SELECT xpath_string($1::text,$2::text)", xml2Compile(detail), false, doc, "[")
		}
	})
}
