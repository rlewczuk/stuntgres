package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsTypesEmptyNotice(input, position, context string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "text-search query doesn't contain lexemes: \"" + input + "\"", Position: position, Context: context}
}

func TestReferenceFTSTypesNotices(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		assert := func(label string, want ...fixture.Diagnostic) {
			t.Helper()
			if len(notices) != len(want) || (len(want) > 0 && !reflect.DeepEqual(notices, want)) {
				t.Fatalf("%s notices=%#v want=%#v", label, notices, want)
			}
			notices = nil
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_notice(id integer,q tsquery)", 0))
		assert("create")
		for _, input := range []string{"", " \t\n"} {
			for _, tc := range []struct {
				name, query, position string
				args                  []any
				runtime, constant     bool
			}{
				{"unknown literal", "SELECT " + ftsTypesQuote(input) + "::tsquery AS q", "8", nil, false, true},
				{"typed literal", "SELECT TSQUERY " + ftsTypesQuote(input) + " AS q", "16", nil, false, true},
				{"qualified literal", "/*é*/ SELECT pg_catalog.tsquery " + ftsTypesQuote(input) + " AS q", "33", nil, false, true},
				{"string bind", "SELECT $1::tsquery AS q", "", []any{input}, false, false},
				{"byte bind", "SELECT $1::tsquery AS q", "", []any{[]byte(input)}, false, false},
				{"runtime literal", "SELECT " + ftsTypesQuote(input) + "::text::tsquery AS q", "", nil, true, true},
				{"runtime bind", "SELECT $1::text::tsquery AS q", "", []any{input}, true, false},
			} {
				t.Run(tc.name+input, func(t *testing.T) {
					context := ""
					if len(tc.args) > 0 && !tc.runtime {
						context = "unnamed portal parameter $1 = '...'"
						if input == "" {
							context = "unnamed portal parameter $1 = ''"
						}
					}
					want := ftsTypesEmptyNotice(input, tc.position, context)
					if _, err := conn.ExecContext(p.Ctx, tc.query, tc.args...); err != nil {
						t.Fatal(err)
					}
					assert("direct Exec", want)
					s := fixture.Select(tc.query, []string{"q"}, []string{"TSQUERY"}, [][]any{{""}}, tc.args...)
					run(s)
					assert("direct Query", want)
					stmt, err := conn.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if tc.constant && !tc.runtime {
						assert("Prepare", want)
					} else {
						assert("Prepare")
					}
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.query}
					for i := range 4 {
						if i < 2 {
							fixture.RunStep(t, p.Ctx, held, recorder, s)
						} else {
							if _, err := stmt.ExecContext(p.Ctx, tc.args...); err != nil {
								t.Fatal(err)
							}
						}
						emit := !tc.constant || tc.runtime
						// PostgreSQL folds constant runtime casts on first Execute.
						// Native retains its existing per-execution expression evaluation.
						if b.PG && tc.runtime && tc.constant && i > 0 {
							emit = false
						}
						if emit {
							assert("held Execute", want)
						} else {
							assert("held Execute")
						}
					}
				})
			}
		}
		run(fixture.Exec("INSERT INTO fts_notice VALUES(1,'')", 1))
		assert("insert", ftsTypesEmptyNotice("", "33", ""))
		run(fixture.Select("SELECT q FROM fts_notice ORDER BY id", []string{"q"}, []string{"TSQUERY"}, [][]any{{""}}))
		assert("stored output")
		for _, args := range [][]any{{nil}, {[]byte(nil)}} {
			run(fixture.Select("SELECT $1::tsquery AS q", []string{"q"}, []string{"TSQUERY"}, [][]any{{nil}}, args...))
			assert("NULL bind")
		}
		run(fixture.Select("SELECT NULL::tsquery AS q", []string{"q"}, []string{"TSQUERY"}, [][]any{{nil}}))
		assert("NULL literal")
		run(fixture.Select("SELECT $1::tsquery AS q", []string{"q"}, []string{"TSQUERY"}, [][]any{{""}}, []byte{}))
		assert("empty bytes", ftsTypesEmptyNotice("", "", "unnamed portal parameter $1 = ''"))
		run(fixture.Exec("SET client_min_messages=warning", 0))
		assert("set warning")
		run(fixture.Select("SELECT ''::tsquery AS q", []string{"q"}, []string{"TSQUERY"}, [][]any{{""}}))
		assert("suppressed literal")
		run(fixture.Select("SELECT $1::tsquery AS q", []string{"q"}, []string{"TSQUERY"}, [][]any{{""}}, ""))
		assert("suppressed bind")
		run(fixture.Exec("SET client_min_messages=notice", 0))
		assert("restore messages")
		run(fixture.Select("SELECT 'a'::tsquery AS q", []string{"q"}, []string{"TSQUERY"}, [][]any{{"'a'"}}))
		assert("no stale notices")
	})
}
