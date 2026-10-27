package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func explainContains(t *testing.T, lines []string, details ...string) {
	t.Helper()
	text := strings.Join(lines, "\n")
	for _, detail := range details {
		if !strings.Contains(text, detail) {
			t.Errorf("plan omits %q:\n%s", detail, text)
		}
	}
}

func explainCosts(t *testing.T, b fixture.Backend, lines []string, enabled bool) {
	t.Helper()
	text := strings.Join(lines, "\n")
	if b.PG {
		if strings.Contains(text, "cost=") != enabled || strings.Contains(text, "rows=") != enabled {
			t.Errorf("COSTS=%t: %s", enabled, text)
		}
		return
	}
	for _, marker := range []string{"estimated rows=", "logical cost=", "Estimates: logical heuristics; not execution measurements"} {
		if strings.Contains(text, marker) != enabled {
			t.Errorf("COSTS=%t, marker %q: %s", enabled, marker, text)
		}
	}
}

func TestReferenceExplainOptions(t *testing.T) {
	cases := []struct {
		options        string
		costs, verbose bool
	}{
		{"COSTS", true, false},
		{"COSTS TRUE", true, false},
		{"COSTS ON", true, false},
		{"COSTS 1", true, false},
		{"COSTS 'true'", true, false},
		{"COSTS FALSE", false, false},
		{"COSTS OFF", false, false},
		{"COSTS 0", false, false},
		{"COSTS 'false'", false, false},
		{"COSTS ON, COSTS OFF", false, false},
		{"COSTS OFF, COSTS ON", true, false},
		{"VERBOSE, COSTS OFF", false, true},
		{"VERBOSE ON, VERBOSE OFF", true, false},
		{"FORMAT TEXT, VERBOSE TRUE", true, true},
		{"FORMAT JSON, FORMAT TEXT", true, false},
		{"ANALYZE TRUE, ANALYZE FALSE", true, false},
		{"ANALYZE OFF, SETTINGS OFF, GENERIC_PLAN OFF, BUFFERS OFF, WAL OFF, TIMING OFF, SUMMARY OFF, MEMORY OFF, SERIALIZE NONE", true, false},
	}
	for _, c := range cases {
		t.Run(c.options, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE SEQUENCE explain_option_s START 1", 0))
					run(fixture.Exec("CREATE TABLE explain_option_t(id integer)", 0))
					run(fixture.Exec("INSERT INTO explain_option_t VALUES(1),(2)", 2))
					q := "EXPLAIN (" + c.options + ") SELECT nextval('explain_option_s'),x.id FROM explain_option_t x WHERE x.id>0 ORDER BY x.id DESC"
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
							lines := explainQuery(t, p, b, q, prepared)
							explainCosts(t, b, lines, c.costs)
							explainContains(t, lines, "explain_option_t")
							if c.verbose {
								explainContains(t, lines, "Output:", "public.explain_option_t", "x.id", "nextval")
							}
						})
					}
					run(fixture.Select("SELECT nextval('explain_option_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Select("SELECT id FROM explain_option_t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			}
		})
	}
}
