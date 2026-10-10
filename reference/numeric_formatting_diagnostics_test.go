package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func numericFormattingDiagnostic(code, message, detail, hint, position string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail, Hint: hint, Position: position}
}

func TestReferenceNumericFormattingTemplateDiagnostics(t *testing.T) {
	// REL_18_0 NUMDesc_prepare complete English diagnostic records; all
	// unspecified fields are explicitly empty, not omitted from comparison.
	cases := []struct{ mask, message, detail string }{
		{"9.9.9", "multiple decimal points", ""}, {"9D9.9", "multiple decimal points", ""},
		{"9V9.9", `cannot use "V" and decimal point together`, ""}, {"9.9V9", `cannot use "V" and decimal point together`, ""},
		{"9SS", `cannot use "S" twice`, ""},
		{"9MIS", `cannot use "S" and "PL"/"MI"/"SG"/"PR" together`, ""},
		{"9PLS", `cannot use "S" and "PL"/"MI"/"SG"/"PR" together`, ""},
		{"9SGS", `cannot use "S" and "PL"/"MI"/"SG"/"PR" together`, ""},
		{"9PRS", `cannot use "S" and "PL"/"MI"/"SG"/"PR" together`, ""},
		{"9SMI", `cannot use "S" and "MI" together`, ""}, {"9SSG", `cannot use "S" and "SG" together`, ""},
		{"9PLPR", `cannot use "PR" and "S"/"PL"/"MI"/"SG" together`, ""},
		{"9MIPR", `cannot use "PR" and "S"/"PL"/"MI"/"SG" together`, ""},
		{"9SGPR", `cannot use "PR" and "S"/"PL"/"MI"/"SG" together`, ""},
		{"PR9", `"9" must be ahead of "PR"`, ""}, {"PR0", `"0" must be ahead of "PR"`, ""},
		{"RNRN", `cannot use "RN" twice`, ""},
		{"MIRN", `"RN" is incompatible with other formats`, `"RN" may only be used together with "FM".`},
		{"RNMI", `"RN" is incompatible with other formats`, `"RN" may only be used together with "FM".`},
		{"RND", `"RN" is incompatible with other formats`, `"RN" may only be used together with "FM".`},
		{"RNV", `"RN" is incompatible with other formats`, `"RN" may only be used together with "FM".`},
		{"9EEEEEEEE", `cannot use "EEEE" twice`, ""}, {"9EEEE9", `"EEEE" must be the last pattern used`, ""},
		{"FM9EEEE", `"EEEE" is incompatible with other formats`, `"EEEE" may only be used together with digit and decimal point patterns.`},
		{"S9EEEE", `"EEEE" is incompatible with other formats`, `"EEEE" may only be used together with digit and decimal point patterns.`},
		{"9V9EEEE", `"EEEE" is incompatible with other formats`, `"EEEE" may only be used together with digit and decimal point patterns.`},
	}
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, v := range cases {
			for _, mask := range []string{v.mask, strings.ToLower(v.mask)} {
				for _, fn := range []string{"to_char", "to_number"} {
					t.Run(fn+"/"+mask, func(t *testing.T) {
						source := "1::numeric"
						if fn == "to_number" {
							source = "'1'::text"
						}
						q := "SELECT " + fn + "(" + source + "," + numericFormattingQuote(mask) + ") AS result"
						s := numericFormattingError(q, numericFormattingDiagnostic("42601", v.message, v.detail, "", ""))
						numericFormattingBoth(t, p, b, c, s)
						// Dynamic masks must also execute the same diagnostic.
						s.query = "SELECT " + fn + "(" + source + ",$1::text) AS result"
						s.args = []any{mask}
						numericFormattingBoth(t, p, b, c, s)
					})
				}
			}
		}
		// Longest-token scanning consumes SP before S: these masks succeed,
		// leaving L as C currency space or R/r as a copied literal.
		for _, v := range []struct{ mask, want string }{{"9SPL", " 1 "}, {"9SPR", " 1R"}, {"9spl", " 1 "}, {"9spr", " 1r"}} {
			t.Run("SP-collision/"+v.mask, func(t *testing.T) {
				numericFormattingSelect(t, p, b, c, "SELECT to_char(1::numeric,$1::text) AS rendered,to_number('1',$1::text) AS parsed", []string{"rendered", "parsed"}, []string{"TEXT", "NUMERIC"}, [][]any{{v.want, "1"}}, v.mask)
				numericFormattingSelect(t, p, b, c, "SELECT to_char(1::numeric,"+numericFormattingQuote(v.mask)+") AS rendered,to_number('1',"+numericFormattingQuote(v.mask)+") AS parsed", []string{"rendered", "parsed"}, []string{"TEXT", "NUMERIC"}, [][]any{{v.want, "1"}})
			})
		}
		for _, source := range []string{"viv", "MMMM", "VV", "IL", "VIX", "qiv", " "} {
			t.Run("roman/"+source, func(t *testing.T) {
				s := referenceErrorDiagnostic("SELECT to_number($1::text,'RN') AS result", numericFormattingDiagnostic("22P02", "invalid Roman numeral", "", "", ""), source)
				s.selectRows = true
				numericFormattingBoth(t, p, b, c, s)
			})
		}
		for _, v := range []struct{ q, code, message string }{
			{"SELECT to_number('','RN') AS result", "22P02", `invalid input syntax for type numeric: " "`},
			{"SELECT to_number('1e2','9EEEE') AS result", "0A000", `"EEEE" not supported for input`},
		} {
			t.Run(v.q, func(t *testing.T) {
				s := referenceErrorDiagnostic(v.q, numericFormattingDiagnostic(v.code, v.message, "", "", ""))
				s.selectRows = true
				numericFormattingBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceNumericFormattingSignatures(t *testing.T) {
	missing := "No function matches the given name and argument types. You might need to add explicit type casts."
	ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, v := range []struct{ q, code, message, hint string }{
			{"SELECT to_char(true,'9')", "42883", "function to_char(boolean, unknown) does not exist", missing},
			{"SELECT to_char(NULL,'9')", "42725", "function to_char(unknown, unknown) is not unique", ambiguous},
			{"SELECT to_char('1','9')", "42725", "function to_char(unknown, unknown) is not unique", ambiguous},
			{"SELECT to_char($1,$2)", "42725", "function to_char(unknown, unknown) is not unique", ambiguous},
			{"SELECT to_number(1,'9')", "42883", "function to_number(integer, unknown) does not exist", missing},
			{"SELECT to_char(1,2)", "42883", "function to_char(integer, integer) does not exist", missing},
		} {
			t.Run(v.q, func(t *testing.T) {
				var args []any
				if strings.Contains(v.q, "$1") {
					args = []any{"12", "FM99"}
				}
				numericFormattingAnalysisError(t, p, b, c, v.q, numericFormattingDiagnostic(v.code, v.message, "", v.hint, "8"), args...)
			})
		}
		for _, v := range []struct{ q, code, message, hint, position string }{
			{"SELECT 'é猫', to_char(true,'9')", "42883", "function to_char(boolean, unknown) does not exist", missing, "14"},
			{"SELECT to_number('1')", "42883", "function to_number(unknown) does not exist", missing, "8"},
			{"SELECT to_number('1','9','9')", "42883", "function to_number(unknown, unknown, unknown) does not exist", missing, "8"},
			{"SELECT missing_nf.to_number('1','9')", "3F000", `schema "missing_nf" does not exist`, "", "8"},
			{`SELECT "TO_CHAR"(1,'9')`, "42883", "function TO_CHAR(integer, unknown) does not exist", missing, "8"},
			{"SELECT to_number(source => '1', template => '9')", "42883", "function to_number(source => unknown, template => unknown) does not exist", missing, "8"},
			{"SELECT to_char(VARIADIC '1'::text)", "42883", "function to_char(text) does not exist", missing, "8"},
			{"SELECT to_number($1,$1::numeric)", "42883", "function to_number(unknown, numeric) does not exist", missing, "8"},
			{"SELECT to_char(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", "", "8"},
			{"SELECT to_char(no_such_nf_column," + strings.Repeat("1,", 99) + "1)", "42703", `column "no_such_nf_column" does not exist`, "", "16"},
		} {
			t.Run(v.q, func(t *testing.T) {
				var args []any
				if strings.Contains(v.q, "$1") {
					args = []any{"12"}
				}
				numericFormattingAnalysisError(t, p, b, c, v.q, numericFormattingDiagnostic(v.code, v.message, "", v.hint, v.position), args...)
			})
		}
		numericFormattingSelect(t, p, b, c, "SELECT to_char($1::numeric,'FM99'),to_char($1::integer,'FM99')", []string{"to_char", "to_char"}, []string{"TEXT", "TEXT"}, [][]any{{"12", "12"}}, "12")
	})
}

func TestReferenceNumericFormattingPhases(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE nf_phase(n numeric,s text,m text)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO nf_phase VALUES(NULL,NULL,NULL),(1,'1','9.9.9')", 2))
		for _, v := range []struct {
			q, name, typ string
			want         any
		}{
			{"SELECT to_char(NULL::numeric,'9.9.9')", "to_char", "TEXT", nil},
			{"SELECT to_number(NULL::text,'9EEEE')", "to_number", "NUMERIC", nil},
			{"SELECT to_char(1::numeric,NULL::text)", "to_char", "TEXT", nil},
			{"SELECT CASE WHEN true THEN 'ok' ELSE to_char(1::numeric,'9.9.9') END", "to_char", "TEXT", "ok"},
		} {
			t.Run(v.q, func(t *testing.T) {
				numericFormattingSelect(t, p, b, c, v.q, []string{v.name}, []string{v.typ}, [][]any{{v.want}})
			})
		}
		for _, v := range []struct{ q, code, message string }{
			{"SELECT to_char(1::numeric,'9.9.9')", "42601", "multiple decimal points"},
			{"SELECT to_number('1','9EEEE')", "0A000", `"EEEE" not supported for input`},
			{"SELECT to_char(NULL::numeric,(1/0)::text)", "22012", "division by zero"},
			{"SELECT to_char((1/0)::numeric,NULL::text)", "22012", "division by zero"},
			{"SELECT to_char(n,(1/0)::text) FROM nf_phase WHERE n IS NULL", "22012", "division by zero"},
			{"SELECT to_char((1/0)::numeric,m) FROM nf_phase WHERE m IS NULL", "22012", "division by zero"},
			{"SELECT to_number(s,(1/0)::text) FROM nf_phase WHERE s IS NULL", "22012", "division by zero"},
			{"SELECT to_char(n,m) FROM nf_phase ORDER BY n NULLS FIRST", "42601", "multiple decimal points"},
			{"SELECT to_char(1::numeric,'9EEEE9.9.9')", "42601", `"EEEE" must be the last pattern used`},
			{"SELECT to_char(1::numeric,'9.9.9EEEE9')", "42601", "multiple decimal points"},
		} {
			t.Run(v.q, func(t *testing.T) {
				numericFormattingBoth(t, p, b, c, numericFormattingError(v.q, numericFormattingDiagnostic(v.code, v.message, "", "", "")))
			})
		}
		numericFormattingAnalysisError(t, p, b, c, "SELECT to_char('bad'::numeric,'9')", numericFormattingDiagnostic("22P02", `invalid input syntax for type numeric: "bad"`, "", "", "16"))
		for _, q := range []string{"SELECT to_number($1,$2) AS result", "SELECT to_char($1::numeric,$2::text) AS result"} {
			t.Run("recovery/"+q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				number := strings.Contains(q, "to_number")
				typ := "TEXT"
				if number {
					typ = "NUMERIC"
				}
				for _, v := range []struct {
					source        any
					mask          string
					want          any
					code, message string
				}{
					{"1", "9.9.9", nil, "42601", "multiple decimal points"},
					{"bad", "RN", nil, "22P02", "invalid Roman numeral"},
					{"12", "FM99", "12", "", ""}, {nil, "9.9.9", nil, "", ""},
					{"bad", "9", nil, "22P02", `invalid input syntax for type numeric: " "`},
					{"12", "FM99", "12", "", ""},
				} {
					if v.code != "" {
						d := numericFormattingDiagnostic(v.code, v.message, "", "", "")
						if !number && v.source == "bad" {
							d.Message = `invalid input syntax for type numeric: "bad"`
							d.Context = "unnamed portal parameter $1 = '...'"
						}
						referenceRunStep(t, p.ctx, held, b.recorder, numericFormattingError(q, d, v.source, v.mask))
					} else {
						s := referenceSelect(q, []string{"result"}, []string{typ}, [][]any{{v.want}}, v.source, v.mask)
						s.checkMetadata = func(t *testing.T, r *sql.Rows) { numericFormattingMetadata(t, r, b.pg) }
						s.checkRaw = func(t *testing.T, i, j int, v any) bool { referenceAssertRawValue(t, b.pg, typ, v); return true }
						referenceRunStep(t, p.ctx, held, b.recorder, s)
					}
				}
			})
		}
	})
}

func TestReferenceNumericFormattingTransactionRecovery(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
		run(referenceExec("SAVEPOINT nf_recover", 0))
		run(referenceErrorDiagnostic("SELECT to_number('IL','RN')", numericFormattingDiagnostic("22P02", "invalid Roman numeral", "", "", "")))
		run(referenceErrorDiagnostic("SELECT to_char(12,'FM99')", numericFormattingDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block", "", "", "")))
		run(referenceExec("ROLLBACK TO nf_recover", 0))
		run(referenceSelect("SELECT to_char(12,'FM99') AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"12"}}))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		// A separate whole-transaction rollback route must recover too.
		tx, err = c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT to_char(1,'9SS')", numericFormattingDiagnostic("42601", `cannot use "S" twice`, "", "", "")))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		numericFormattingBoth(t, p, b, c, referenceSelect("SELECT to_number('12','99') AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{"12"}}))
	})
}

func TestReferenceNumericFormattingHeldMaskRecovery(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, fn := range []string{"to_char", "to_number"} {
			t.Run(fn, func(t *testing.T) {
				source, typ := "$1::numeric", "TEXT"
				if fn == "to_number" {
					source, typ = "$1::text", "NUMERIC"
				}
				q := fmt.Sprintf("SELECT %s(%s,$2::text) AS result", fn, source)
				stmt, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				s := referenceErrorDiagnostic(q, numericFormattingDiagnostic("42601", "multiple decimal points", "", "", ""), "12", "9.9.9")
				s.selectRows, s.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, held, b.recorder, s)
				referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"result"}, []string{typ}, [][]any{{"12"}}, "12", "FM99"))
			})
		}
	})
}

func TestReferenceNumericFormattingIntegerWrapperErrors(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		d := numericFormattingDiagnostic("22003", "bigint out of range", "", "", "")
		for _, q := range []string{
			"SELECT to_char(9223372036854775807::bigint,'FM9999999999999999999V9') AS result",
			"SELECT to_char(1::bigint,'FM9V9999999999999999999') AS result",
		} {
			numericFormattingBoth(t, p, b, c, numericFormattingError(q, d))
		}
		q := "SELECT to_char($1::bigint,$2::text) AS result"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, numericFormattingError(q, d, "9223372036854775807", "FM9999999999999999999V9"))
		referenceRunStep(t, p.ctx, held, b.recorder, numericFormattingError(q, d, "1", "FM9V9999999999999999999"))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"1000000000000000000"}}, "1", "FM9V999999999999999999"))
	})
}
