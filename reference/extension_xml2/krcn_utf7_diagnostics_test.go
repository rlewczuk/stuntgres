package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Unclosed-root inputs make document rejection unambiguous. A completed root
// followed by converter faults has a separate upstream pointer/projection policy
// and is deliberately not inferred from the package's strict failure contract.
func xml2KRCNUTF7Malformed() []string {
	return []string{
		xml2RawDocument("ISO-2022-KR", "\x1b$)C\x0e~!\x0f"),
		xml2RawDocument("ISO-2022-KR", "\x1b$)C\x0eI!\x0f"),
		xml2RawDocument("ISO-2022-KR", "\x1b$X"),
		xml2RawDocument("ISO-2022-CN", "\x1b$)A\x0e~!\x0f"),
		xml2RawDocument("ISO-2022-CN", "\x1bN !"),
		xml2RawDocument("ISO-2022-CN", "\x1b$X"),
		xml2RawDocument("UTF-7", "+A-"),
		xml2RawDocument("UTF-7", "+AA-"),
		xml2RawDocument("UTF-7", "+AOl-"),
		xml2RawDocument("UTF-7", "+3AA-"),
		xml2RawDocument("UTF-7", "+2AA-"),
		xml2RawDocument("UTF-7", "+2AAAQQ-"),
		xml2RawDocument("UTF-7", "+AAA-"),
		xml2RawDocument("UTF-7", "+ABs-"),
		xml2RawDocument("UTF-7", "~"),
		xml2RawDocument("UTF-7", "\\"),
		xml2RawDocument("BOGUS", "12"),
		`<?xml version="1.0" encoding="ISO-2022-KR"?><r>`,
		`<?xml version="1.0" encoding="ISO-2022-CN"?><r>`,
		`<?xml version="1.0" encoding="UTF-7"?><r>`,
	}
}

func TestReferenceXML2KRCNUTF7Diagnostics(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, fn := range []string{"xpath_string", "xpath_number", "xpath_bool"} {
			q := "SELECT " + fn + "($1::text,$2::text) AS value"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal("parameterized prepare must be quiet", err) }
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			typ, healthy := "TEXT", any("12")
			if fn == "xpath_number" { typ, healthy = "FLOAT4", float64(12) }
			if fn == "xpath_bool" { typ, healthy = "BOOL", true }
			for _, tc := range xml2KRCNUTF7Cases()[:3] {
				doc := xml2RawDocument(tc.label, tc.body)
				xml2Failure(t, p, b, c, q, xml2Compile("Invalid expression"), false, doc, "[")
				failure := fixture.ErrorDiagnostic(q, xml2Compile("Invalid expression"), doc, "[")
				failure.SelectRows, failure.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, h, b.Recorder, failure)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"value"}, []string{typ}, [][]any{{healthy}}, xml2RawDocument(tc.label, "12"), "."))
			}
			for _, doc := range xml2KRCNUTF7Malformed() {
				value := any(nil)
				if fn == "xpath_bool" { value = false }
				// Parsing fails before compiling the bad XPath. Suppressed reader
				// callbacks are not notices, errors or guessed SQLSTATEs.
				s := fixture.Select(q, []string{"value"}, []string{typ}, [][]any{{value}}, doc, "[")
				xml2Both(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			}
			for _, args := range [][2]any{{nil, "["}, {xml2KRCNUTF7Malformed()[0], nil}, {xml2RawDocument("UTF-7", "+AOk-"), nil}, {nil, nil}} {
				s := fixture.Select(q, []string{"value"}, []string{typ}, [][]any{{nil}}, args[0], args[1])
				xml2Both(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"value"}, []string{typ}, [][]any{{healthy}}, xml2RawDocument("UTF-7", "12"), "."))
			if err := stmt.Close(); err != nil { t.Fatal(err) }
		}
	})
}
