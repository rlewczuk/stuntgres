package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankDiagnostics(t *testing.T) {
	for _, name := range ftsRankNames {
		t.Run(name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				query := "SELECT " + name + "($1::real[],$2::tsvector,$3::tsquery,$4::integer)"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, tc := range []struct{ weights, code, message string }{
					{"{}", "2202E", "array of weight must be one-dimensional"},
					{"{{0.1,0.2},{0.4,1}}", "2202E", "array of weight must be one-dimensional"},
					{"{0.1,0.2,0.4}", "2202E", "array of weight is too short"},
					{"{NULL}", "2202E", "array of weight is too short"},
					{"{0.1,0.2,NULL,1}", "22004", "array of weight must not contain nulls"},
					{"{0.1,0.2,0.4,1,NULL}", "22004", "array of weight must not contain nulls"},
					{"{2,0.2,0.4,1,NULL}", "22004", "array of weight must not contain nulls"},
					{"{1.0001,0.2,0.4,1}", "22023", "weight out of range"},
					{"{0.1,0.2,0.4,Infinity}", "22023", "weight out of range"},
				} {
					t.Run(tc.weights, func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						ftsRankDiagnosticRoutes(t, p, b, c, query, d, false, tc.weights, "cat:1", "cat", 0)
						for range 2 {
							for _, selectRows := range []bool{false, true} {
								s := fixture.ErrorDiagnostic(query, d, tc.weights, "cat:1", "cat", 0)
								s.SelectRows, s.ErrorStage = selectRows, "execute"
								fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							}
						}
						want := ftsRankExact(.06079271)
						if name == "ts_rank_cd" {
							want = ftsRankExact(.1)
						}
						rows, err := stmt.QueryContext(p.Ctx, "{0.1,0.2,0.4,1}", "cat:1", "cat", 0)
						if err != nil {
							t.Fatal(err)
						}
						ftsRankRead(t, p, b, rows, name, "FLOAT4", want, false)
					})
				}
				for _, tc := range []struct{ args, types string }{
					{"", ""}, {"1,2", "integer, integer"},
					{"'cat'::text,'cat'::tsquery", "text, tsquery"},
					{"'cat'::tsvector,'cat'::text", "tsvector, text"},
					{"ARRAY['a']::text[],'cat'::tsvector,'cat'::tsquery", "text[], tsvector, tsquery"},
					{"NULL::tsvector,true", "tsvector, boolean"},
					{"'cat'::tsvector,'cat'::tsquery,true", "tsvector, tsquery, boolean"},
					{"'cat'::tsvector,'cat'::tsquery,0,0", "tsvector, tsquery, integer, integer"},
					{"document => 'cat'::tsvector, query => 'cat'::tsquery", "document => tsvector, query => tsquery"},
				} {
					ftsRankDiagnosticRoutes(t, p, b, c, "SELECT "+name+"("+tc.args+")", ftsRankMissing(name+"("+tc.types+")", 8), true)
				}
				ftsRankDecorations(t, p, b, c, name, "'cat'::tsvector", "tsvector", "0")
				// SQL PREPARE is a utility: client Parse does not analyze the
				// enclosed SELECT; its diagnostic is an Execute-phase failure.
				utility := "PREPARE rank_bad AS SELECT " + name + "(1,2)"
				ftsRankDiagnosticRoutes(t, p, b, c, utility, ftsRankMissing(name+"(integer, integer)", strings.Index(utility, name)+1), false)
				unicodeSQL := "/*é🙂*/ SELECT " + name + "(1,2)"
				position := utf8.RuneCountInString(unicodeSQL[:strings.Index(unicodeSQL, name)]) + 1
				ftsRankDiagnosticRoutes(t, p, b, c, unicodeSQL, ftsRankMissing(name+"(integer, integer)", position), true)
				for _, tc := range []struct {
					sql, code, message string
					position           int
				}{
					{"SELECT " + name + "(NULL::tsvector,'a &'::tsquery)", "42601", `no operand in tsquery: "a &"`, 0},
					{"SELECT " + name + "('a:0'::tsvector,NULL::tsquery)", "42601", `wrong position info in tsvector: "a:0"`, 0},
				} {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					literal := "'a &'"
					if tc.code != "42601" || strings.Contains(tc.sql, "'a:0'") {
						literal = "'a:0'"
					}
					d.Position = fixture.Position(strings.Index(tc.sql, literal) + 1)
					ftsRankDiagnosticRoutes(t, p, b, c, tc.sql, d, true)
				}
				// Bind input errors retain the parameter context; runtime function
				// errors above have no guessed blanket Position or Context stripping.
				for _, tc := range []struct {
					index                int
					input, code, message string
				}{
					{1, "{oops,0.2,0.4,1}", "22P02", `invalid input syntax for type real: "oops"`},
					{2, "a:0", "42601", `wrong position info in tsvector: "a:0"`},
					{3, "a &", "42601", `no operand in tsquery: "a &"`},
					{4, "oops", "22P02", `invalid input syntax for type integer: "oops"`},
				} {
					args := []any{"{0.1,0.2,0.4,1}", "cat:1", "cat", 0}
					args[tc.index-1] = tc.input
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Context = "unnamed portal parameter $" + string(rune('0'+tc.index)) + " = '...'"
					ftsRankDiagnosticRoutes(t, p, b, c, query, d, false, args...)
				}
				malformed := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
				malformed.Detail = `Unexpected "," character.`
				malformed.Context = "unnamed portal parameter $1 = '...'"
				ftsRankDiagnosticRoutes(t, p, b, c, query, malformed, false, "{1,,2}", "cat:1", "cat", 0)
				literalArray := "SELECT " + name + "('{1,,2}'::real[],NULL::tsvector,'cat'::tsquery)"
				malformed.Context = ""
				malformed.Position = fixture.Position(strings.Index(literalArray, "'{1,,2}'") + 1)
				ftsRankDiagnosticRoutes(t, p, b, c, literalArray, malformed, true)
				// Constants fail during execution/planning rather than Parse, as
				// distinguished from malformed type input. Root pins this phase.
				ftsRankDiagnosticRoutes(t, p, b, c, "SELECT "+name+"('{1}'::real[],'cat:1'::tsvector,'cat'::tsquery)", fixture.ControlsReferenceDiagnostic("2202E", "array of weight is too short"), false)
			})
		})
	}
}
