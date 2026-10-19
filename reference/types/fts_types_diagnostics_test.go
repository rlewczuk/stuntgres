package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSTypesDiagnostics(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_errors(id integer,v tsvector,q tsquery)", 0))
		for _, tc := range []struct{ typ, input, kind string }{
			{"tsvector", "''", "syntax"}, {"tsvector", "a:0", "position"}, {"tsvector", "a:", "syntax"}, {"tsvector", "a:-1", "syntax"}, {"tsvector", "a:1Z", "syntax"}, {"tsvector", "a:1,", "syntax"}, {"tsvector", "a\\", "escape"},
			{"tsquery", "foo!", "syntax"}, {"tsquery", "a b", "syntax"}, {"tsquery", "a &", "operand"}, {"tsquery", "!", "operand"}, {"tsquery", "(", "operand"}, {"tsquery", "()", "syntax"}, {"tsquery", "a)", "syntax"}, {"tsquery", "a\\", "escape"},
			{"tsquery", "a <16385> b", "distance"}, {"tsquery", "a <-1> b", "syntax"}, {"tsquery", "a < 1> b", "syntax"}, {"tsquery", "a <1 > b", "syntax"}, {"tsquery", "a <> b", "syntax"}, {"tsquery", "a <x> b", "syntax"}, {"tsquery", "a:Z", "syntax"},
		} {
			t.Run(tc.typ+"_"+tc.input, func(t *testing.T) {
				code := "42601"
				message := "syntax error in " + tc.typ + ": \"" + tc.input + "\""
				switch tc.kind {
				case "position":
					message = "wrong position info in tsvector: \"" + tc.input + "\""
				case "escape":
					message = "there is no escaped character: \"" + tc.input + "\""
				case "operand":
					message = "no operand in tsquery: \"" + tc.input + "\""
				case "distance":
					code = "22023"
					message = "distance in phrase operator must be an integer value between zero and 16384 inclusive"
				}
				base := fixture.ControlsReferenceDiagnostic(code, message)
				for _, query := range []string{
					"SELECT " + ftsTypesQuote(tc.input) + "::" + tc.typ,
					"/*é*/ SELECT " + ftsTypesQuote(tc.input) + "::" + tc.typ,
					"SELECT " + tc.typ + " " + ftsTypesQuote(tc.input),
					"SELECT pg_catalog." + tc.typ + " " + ftsTypesQuote(tc.input),
					"SELECT CAST(" + ftsTypesQuote(tc.input) + " AS " + tc.typ + ")",
					"SELECT " + ftsTypesQuote(tc.input) + "::text::" + tc.typ,
					"SELECT " + ftsTypesQuote(tc.input) + "::" + tc.typ + " FROM fts_errors",
					"SELECT " + ftsTypesQuote(tc.input) + "::" + tc.typ + " FROM fts_errors WHERE FALSE",
				} {
					runtime := strings.Contains(query, "::text::")
					d := base
					if !runtime {
						d.Position = fixture.Position(1 + utf8.RuneCountInString(query[:strings.Index(query, "'")]))
					}
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(query, d)
							s.SelectRows = queryMode
							s.Prepared = prepared
							s.ErrorStage = "execute"
							if prepared && !runtime {
								s.ErrorStage = "prepare"
							}
							run(s)
						}
					}
				}
				for _, arg := range []any{tc.input, []byte(tc.input)} {
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							d := base
							d.Context = "unnamed portal parameter $1 = '...'"
							s := fixture.ErrorDiagnostic("SELECT $1::"+tc.typ, d, arg)
							s.SelectRows = queryMode
							s.Prepared = prepared
							s.ErrorStage = "execute"
							run(s)
						}
					}
				}
			})
		}
		for _, tc := range []struct{ query, code, message, position string }{
			{"SELECT 1::tsvector", "42846", "cannot cast type integer to tsvector", "9"},
			{"SELECT 1::tsquery", "42846", "cannot cast type integer to tsquery", "9"},
			{"SELECT 'a'::tsvector(2)", "42601", "type modifier is not allowed for type \"tsvector\"", "13"},
			{"SELECT 'a'::tsquery(2)", "42601", "type modifier is not allowed for type \"tsquery\"", "13"},
			{"SELECT 'a'::tsvector COLLATE \"C\"", "42804", "collations are not supported by type tsvector", "22"},
			{"SELECT 'a'::tsquery COLLATE \"C\"", "42804", "collations are not supported by type tsquery", "21"},
		} {
			for _, prepared := range []bool{false, true} {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = tc.position
				s := fixture.ErrorDiagnostic(tc.query, d)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		run(fixture.Exec("INSERT INTO fts_errors VALUES(1,'a','a')", 1))
		for _, tc := range []struct{ column, input, message string }{
			{"v", "a:0", "wrong position info in tsvector: \"a:0\""},
			{"q", "a &", "no operand in tsquery: \"a &\""},
		} {
			query := "INSERT INTO fts_errors(id," + tc.column + ") VALUES(2,'b'),(3," + ftsTypesQuote(tc.input) + ")"
			d := fixture.ControlsReferenceDiagnostic("42601", tc.message)
			d.Position = fixture.Position(strings.Index(query, ftsTypesQuote(tc.input)) + 1)
			run(fixture.ErrorDiagnostic(query, d))
			d.Position = ""
			d.Context = "unnamed portal parameter $2 = '...'"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO fts_errors(id,"+tc.column+") VALUES(2,$1),(3,$2)", d, "b", tc.input)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT id,v,q FROM fts_errors ORDER BY id", []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, [][]any{{int64(1), "'a'", "'a'"}}))
			}
		}
	})
}
