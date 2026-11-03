package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineCollation(t *testing.T) {
	// PostgreSQL18 §23.2.1 derives a collatable function result from its
	// arguments even when its byte computation does not use collation.
	// https://www.postgresql.org/docs/18/collation.html#COLLATION-CONCEPTS
	// C/POSIX are distinct identities despite equivalent byte ordering.
	conflict := func(query, left, right string) fixture.Diagnostic {
		d := fixture.ControlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "`+left+`" and "`+right+`"`)
		later := strings.LastIndex(query, "COLLATE")
		d.Position = fixture.Position(utf8.RuneCountInString(query[:later]) + 1)
		return d
	}
	for form, types := range ftsHeadlineForms {
		explicit := types[0] == "regconfig"
		options := types[len(types)-1] == "text"
		call := func(document, query, option string) string {
			arguments := document + "," + query
			if explicit {
				arguments = "'english'::regconfig," + arguments
			}
			if options {
				arguments += "," + option
			}
			return "ts_headline(" + arguments + ")"
		}
		type collationCase struct{ name, document, options, derived string }
		cases := []collationCase{
			{"document_c", "C", "", "C"},
			{"document_posix", "POSIX", "", "POSIX"},
		}
		if options {
			cases = append(cases,
				collationCase{"options_c", "", "C", "C"},
				collationCase{"options_posix", "", "POSIX", "POSIX"},
				collationCase{"matching_c", "C", "C", "C"},
				collationCase{"matching_posix", "POSIX", "POSIX", "POSIX"},
			)
		}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("form_%d/%s", form, tc.name), func(t *testing.T) {
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					defer ftsConvertSetting(t, p, c)()
					document, option := "'Cat'::text", "''::text"
					parameterDocument, parameterOption := "$1::text", "$3::text"
					if tc.document != "" {
						clause := ` COLLATE "` + tc.document + `"`
						document += clause
						parameterDocument += clause
					}
					if tc.options != "" {
						clause := ` COLLATE "` + tc.options + `"`
						option += clause
						parameterOption += clause
					}
					expression := call(document, "'cat'::tsquery", option)
					ftsRankRoutes(t, p, b, c, "SELECT "+expression+" AS excerpt", "excerpt", "TEXT", "<b>Cat</b>")
					// A compatible surrounding comparison is ordinary SQL, not an
					// unselected inquiry function or an output-collation override.
					comparison := "SELECT (" + expression + `) = ('<b>Cat</b>'::text COLLATE "` + tc.derived + `") AS matched`
					s := fixture.Select(comparison, []string{"matched"}, []string{"BOOL"}, [][]any{{true}})
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					parameterExpression := call(parameterDocument, "$2::tsquery", parameterOption)
					query := "SELECT " + parameterExpression + " AS excerpt"
					held, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					for _, value := range []struct {
						document, query, options any
						want                     any
					}{
						{"Cat", "cat", "", "<b>Cat</b>"},
						{nil, "cat", "", nil},
						{"Rat", "rat", "", "<b>Rat</b>"},
						{"Cat", nil, "", nil},
						{"Cat", "cat", "", "<b>Cat</b>"},
					} {
						args := []any{value.document, value.query}
						if options {
							args = append(args, value.options)
						}
						ftsRankRoutes(t, p, b, c, query, "excerpt", "TEXT", value.want, args...)
						rows, err := held.QueryContext(p.Ctx, args...)
						if err != nil {
							t.Fatal(err)
						}
						ftsRankRead(t, p, b, rows, "excerpt", "TEXT", value.want, false)
					}
					if options {
						for _, value := range []struct {
							options any
							want    any
						}{
							{nil, nil},
							{"StartSel=[,StopSel=],HighlightAll=true", "[Cat]"},
							{"", "<b>Cat</b>"},
						} {
							args := []any{"Cat", "cat", value.options}
							ftsRankRoutes(t, p, b, c, query, "excerpt", "TEXT", value.want, args...)
							rows, err := held.QueryContext(p.Ctx, args...)
							if err != nil {
								t.Fatal(err)
							}
							ftsRankRead(t, p, b, rows, "excerpt", "TEXT", value.want, false)
						}
					}
					// The result must retain its argument's explicit derivation
					// through a surrounding collation-sensitive operator.
					other := "POSIX"
					if tc.derived == "POSIX" {
						other = "C"
					}
					outer := "SELECT (" + parameterExpression + `) = ('<b>Cat</b>'::text COLLATE "` + other + `") AS matched`
					args := []any{"Cat", "cat"}
					if options {
						args = append(args, "")
					}
					ftsRankDiagnosticRoutes(t, p, b, c, outer, conflict(outer, tc.derived, other), true, args...)
					// Failed analysis on the same connection must not invalidate
					// the retained, correctly collated headline definition.
					rows, err := held.QueryContext(p.Ctx, args...)
					if err != nil {
						t.Fatal(err)
					}
					ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Cat</b>", false)
					compatible := "SELECT (" + parameterExpression + `) = ('<b>Cat</b>'::text COLLATE "` + tc.derived + `") AS matched`
					comparisonHeld, err := c.PrepareContext(p.Ctx, compatible)
					if err != nil {
						t.Fatal(err)
					}
					defer comparisonHeld.Close()
					for _, value := range []struct {
						document any
						want     any
					}{{"Cat", true}, {nil, nil}, {"Cat", true}} {
						arguments := append([]any(nil), args...)
						arguments[0] = value.document
						step := fixture.Select(compatible, []string{"matched"}, []string{"BOOL"}, [][]any{{value.want}}, arguments...)
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: comparisonHeld, SQL: compatible}, b.Recorder, step)
					}
				})
			})
		}
	}
	// Conflicting arguments are binding errors, including valid NULL inputs.
	// Keep exact reviewer SQL and a Unicode prefix to distinguish characters
	// from bytes at the later conflicting COLLATE clause.
	for _, explicit := range []bool{false, true} {
		for _, pair := range [][2]string{{"C", "POSIX"}, {"POSIX", "C"}} {
			for _, prefix := range []string{"", "/* é🙂 */ "} {
				for _, nullDocument := range []bool{false, true} {
					name := fmt.Sprintf("conflict/explicit_%t/%s_%s/unicode_%t/null_%t", explicit, pair[0], pair[1], prefix != "", nullDocument)
					t.Run(name, func(t *testing.T) {
						ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							document := "'Cat'::text"
							if nullDocument {
								document = "NULL::text"
							}
							arguments := document + ` COLLATE "` + pair[0] + `",'cat'::tsquery,''::text COLLATE "` + pair[1] + `"`
							if explicit {
								arguments = "'english'::regconfig," + arguments
							}
							query := prefix + "SELECT ts_headline(" + arguments + ")"
							ftsRankDiagnosticRoutes(t, p, b, c, query, conflict(query, pair[0], pair[1]), true)
						})
					})
				}
			}
		}
	}
	for _, tc := range []struct{ name, expression, collation string }{
		{"document_c", `ts_headline('english'::regconfig,'Cat'::text COLLATE "C",'cat'::tsquery)`, "C"},
		{"options_posix", `ts_headline('english'::regconfig,'Cat'::text,'cat'::tsquery,''::text COLLATE "POSIX")`, "POSIX"},
	} {
		t.Run("stored/"+tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertSetting(t, p, c)()
				name := "public.fts_headline_coll_" + tc.name
				create := "CREATE VIEW " + name + " AS SELECT " + tc.expression + " AS v"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(create, 0))
				metadata := "SELECT a.atttypid=25::oid AS text_result,a.atttypmod AS typmod,n.nspname::text AS namespace,c.collname::text AS collation FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_collation c ON c.oid=a.attcollation JOIN pg_catalog.pg_namespace n ON n.oid=c.collnamespace WHERE a.attrelid=$1::regclass AND a.attnum=1 AND a.attname='v' AND NOT a.attisdropped"
				want := [][]any{{true, int64(-1), "pg_catalog", tc.collation}}
				step := fixture.Select(metadata, []string{"text_result", "typmod", "namespace", "collation"}, []string{"BOOL", "INT4", "TEXT", "TEXT"}, want, name)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
				// CREATE VIEW's source command is closed; its result-column
				// identity and bytes must survive fresh and held consumers.
				viewQuery := "SELECT v FROM " + name
				ftsRankRoutes(t, p, b, c, viewQuery, "v", "TEXT", ftsRankViewWant{value: "<b>Cat</b>"})
				held, err := c.PrepareContext(p.Ctx, viewQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				catalogHeld, err := c.PrepareContext(p.Ctx, metadata)
				if err != nil {
					t.Fatal(err)
				}
				defer catalogHeld.Close()
				for _, setting := range []string{"simple", "english"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
					ftsRankRoutes(t, p, b, c, viewQuery, "v", "TEXT", ftsRankViewWant{value: "<b>Cat</b>"})
					rows, err := held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					ftsRankRead(t, p, b, rows, "v", "TEXT", ftsRankViewWant{value: "<b>Cat</b>"}, false)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: catalogHeld, SQL: metadata}, b.Recorder, step)
				}
			})
		})
	}
	t.Run("conflicting_view_no_publication", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			const name = "public.fts_headline_coll_rejected"
			query := `/* é🙂 */ CREATE VIEW ` + name + ` AS SELECT ts_headline('Cat'::text COLLATE "C",'cat'::tsquery,''::text COLLATE "POSIX") AS v`
			// Utility Parse retains its SELECT; the conflict is at Execute.
			ftsRankDiagnosticRoutes(t, p, b, c, query, conflict(query, "C", "POSIX"), false)
			absent := fixture.Select("SELECT count(*)::bigint AS relations FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='fts_headline_coll_rejected'", []string{"relations"}, []string{"INT8"}, [][]any{{int64(0)}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, absent)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(absent))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW `+name+` AS SELECT ts_headline('english'::regconfig,'Cat'::text COLLATE "C",'cat'::tsquery) AS v`, 0))
			ftsRankRoutes(t, p, b, c, "SELECT v FROM "+name, "v", "TEXT", ftsRankViewWant{value: "<b>Cat</b>"})
		})
	})
}
