package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectGrammarPositions(t *testing.T) {
	for _, prefix := range []string{"", "/* é界 */ "} {
		t.Run(fmt.Sprint(prefix != ""), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
					run(fixture.Exec("CREATE STATISTICS sentinel_s ON a,b FROM stats_t", 0))
					for _, tc := range []struct{ q, token string }{
						{"CREATE STATISTICS", ""},
						{"CREATE STATISTICS s ON a,b FROM", ""},
						{"CREATE STATISTICS s () ON a,b FROM stats_t", ")"},
						{"CREATE STATISTICS s (,) ON a,b FROM stats_t", ","},
						{"CREATE STATISTICS s (mcv,) ON a,b FROM stats_t", ")"},
						{"CREATE STATISTICS s ON FROM stats_t", "FROM"},
						{"CREATE STATISTICS s ON a, FROM stats_t", "FROM"},
						{"CREATE STATISTICS IF EXISTS s ON a,b FROM stats_t", "EXISTS"},
						{"CREATE STATISTICS s ON a,b FROM stats_t )", ")"},
						{"DROP STATISTICS", ""},
						{"DROP STATISTICS IF NOT EXISTS sentinel_s", "NOT"},
						{"DROP STATISTICS sentinel_s,", ""},
						{"DROP STATISTICS sentinel_s RESTRICT CASCADE", "CASCADE"},
					} {
						q := prefix + tc.q
						offset := len(q)
						message := "syntax error at end of input"
						if tc.token != "" {
							offset = strings.Index(q, tc.token)
							message = `syntax error at or near "` + tc.token + `"`
						}
						want := fixture.ControlsReferenceDiagnostic("42601", message)
						want.Position = strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(q, want)
							if prepared {
								s = fixture.Prepared(s)
								s.ErrorStage = "prepare"
							} else {
								s.ErrorStage = "execute"
							}
							run(s)
							run(statisticsRows([]any{"sentinel_s", "1 2", "{d,f,m}", nil, nil}))
						}
					}
				})
			}
		})
	}
}

func TestReferenceStatisticsObjectOrdinaryOwnerBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
			run(fixture.Exec("CREATE STATISTICS sentinel_s ON a,b FROM stats_t", 0))
			// Native-only capability boundary; general PostgreSQL ALTER is not excluded.
			if !b.PG {
				for _, prefix := range []string{"", "/* é界 */ "} {
					for _, prepared := range []bool{false, true} {
						want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported")
						want.Position = strconv.Itoa(utf8.RuneCountInString(prefix) + 1)
						s := fixture.ErrorDiagnostic(prefix+"ALTER STATISTICS public.sentinel_s OWNER TO CURRENT_USER", want)
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						} else {
							s.ErrorStage = "execute"
						}
						run(s)
					}
				}
			}
			run(statisticsRows([]any{"sentinel_s", "1 2", "{d,f,m}", nil, nil}))
		})
	}
}
