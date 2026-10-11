package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringCoreReferenceSyntax(t *testing.T, p *fixture.Pair) {
	steps := []fixture.Step{}
	for _, tc := range []stringCoreReferenceCase{
		{"SELECT ltrim(NULL) AS v", nil, "TEXT"},
		{"SELECT rtrim('x',NULL) AS v", nil, "TEXT"},
		{"SELECT trim(leading from ' x ') AS v", "x ", "TEXT"},
		{"SELECT trim(trailing from ' x ') AS v", " x", "TEXT"},
		{"SELECT btrim('a\t  '::char(8),'a') AS v", "\t ", "TEXT"},
		{"SELECT \"normalize\"('x','NFC '::char(5)) AS v", "x", "TEXT"},
		{"SELECT normalize('é',nFc) AS v", "é", "TEXT"},
		{"SELECT 'ⓑ' IS NFKC NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'b' IS NFKC NORMALIZED AS v", true, "BOOL"},
		{"SELECT 'é' IS NFKD NORMALIZED AS v", false, "BOOL"},
		{"SELECT 'é' IS NFKD NORMALIZED AS v", true, "BOOL"},
		{"SELECT is_normalized('é','nFd') AS v", true, "BOOL"},
		{"SELECT normalize('A̅́') AS v", "A̅́", "TEXT"},
		{"SELECT normalize('̀̕',NFD) AS v", "̀̕", "TEXT"},
		{"SELECT 'x'::text||setseed(NULL) AS v", nil, "TEXT"},
		{"SELECT setseed(NULL)||'x'::text AS v", nil, "TEXT"},
	} {
		steps = append(steps, stringCoreSelect(tc))
	}
	steps = append(steps,
		fixture.Select("SELECT trim(' x '),trim(leading from ' x '),trim(trailing from ' x ')", []string{"btrim", "ltrim", "rtrim"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"x", "x ", " x"}}),
		fixture.Select("SELECT normalize('é'),'é' IS NORMALIZED,'é' IS NOT NORMALIZED,'a'||'b'", []string{"normalize", "is_normalized", "?column?", "?column?"}, []string{"TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{"é", true, true, "ab"}}),
	)
	for _, s := range steps {
		for _, prepared := range []bool{false, true} {
			next := s
			if prepared {
				next = fixture.Prepared(next)
			}
			p.Step(t, next)
		}
	}
}
func stringCoreReferenceDiagnosticParameters(t *testing.T, p *fixture.Pair) {
	for _, name := range []string{"btrim", "ltrim", "rtrim", "is_normalized", `"normalize"`} {
		tc := stringCoreDiagnosticCase{"SELECT " + name + "(" + strings.Repeat("'x',", 100) + "'x')", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}, true}
		for _, prepared := range []bool{false, true} {
			s := stringCoreDiagnosticStep(tc, prepared)
			p.Step(t, s)
		}
	}
	var held []fixture.HeldStatement
	for _, backend := range p.Backends() {
		statement, err := backend.DB.PrepareContext(p.Ctx, `SELECT "normalize"($1,$2)`)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held = append(held, fixture.HeldStatement{backend.DB, statement, `SELECT "normalize"($1,$2)`})
	}
	for _, tc := range []struct {
		q    string
		args []any
		want fixture.Diagnostic
	}{
		{`SELECT "normalize"($1,$2)`, []any{"x", "bad"}, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad"}},
		{`SELECT "normalize"($1,$2)`, []any{"x", "NFC "}, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: NFC "}},
		{`SELECT "normalize"($1,$2)`, []any{string([]byte{0}), "NFC"}, fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`, Context: "unnamed portal parameter $1"}},
		{`SELECT "normalize"($1,$2)`, []any{string([]byte{255}), "NFC"}, fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0xff`, Context: "unnamed portal parameter $1"}},
	} {
		step := fixture.ImplicitError(tc.q, tc.want, tc.args...)
		p.Step(t, step)
		for i, backend := range p.Backends() {
			fixture.RunStep(t, p.Ctx, held[i], backend.Recorder, step)
		}
	}
}
