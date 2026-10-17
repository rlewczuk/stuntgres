package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateIntoFrontiers(t *testing.T) {
	for _, operator := range []string{"UNION ALL", "INTERSECT", "EXCEPT"} {
		for _, prepared := range []bool{false, true} {
			t.Run(operator+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						_, run := fixture.QueryTableCreateSession(t, p, b)
						second := "2"
						count := int64(1)
						values := [][]any{{int64(1)}}
						if operator == "INTERSECT" {
							second = "1"
						}
						if operator == "UNION ALL" {
							count = 2
							values = [][]any{{int64(1)}, {int64(2)}}
						}
						q := "SELECT 1 AS i INTO qc_frontier_dst " + operator + " SELECT " + second
						step := fixture.Exec(q, count)
						step.Prepared = prepared
						run(step)
						result := fixture.Select("SELECT i FROM qc_frontier_dst", []string{"i"}, []string{"INT4"}, values)
						result.Bag = true
						run(result)
						run(fixture.Exec("DROP TABLE qc_frontier_dst", 0))
						empty := fixture.Exec("SELECT 1 AS i INTO qc_frontier_dst WHERE false "+operator+" SELECT "+second+" WHERE false", 0)
						empty.Prepared = prepared
						run(empty)
						run(fixture.Select("SELECT i FROM qc_frontier_dst", []string{"i"}, []string{"INT4"}, nil))
					})
				}
			})
		}
	}
	cases := []struct {
		name, query, message, target string
		view                         bool
	}{
		{"right_arm", "SELECT 1 AS i UNION ALL SELECT 2 INTO qc_frontier_dst", "INTO is only allowed on first SELECT of UNION/INTERSECT/EXCEPT", "qc_frontier_dst", false},
		{"ctas_body", "CREATE TABLE qc_frontier_table AS SELECT 1 AS i INTO qc_frontier_dst UNION ALL SELECT 2", "SELECT ... INTO is not allowed here", "qc_frontier_dst", false},
		{"view_body", "CREATE VIEW qc_frontier_view AS SELECT 1 AS i INTO qc_frontier_dst UNION ALL SELECT 2", "views must not contain SELECT INTO", "", true},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						_, run := fixture.QueryTableCreateSession(t, p, b)
						code := "42601"
						if tc.view {
							code = "0A000"
						}
						want := fixture.ControlsReferenceDiagnostic(code, tc.message)
						if tc.target != "" {
							want.Position = fixture.Position(strings.Index(tc.query, tc.target) + 1)
						}
						step := fixture.ErrorDiagnostic(tc.query, want)
						step.Prepared = prepared
						step.ErrorStage = "execute"
						if prepared && !tc.view {
							step.ErrorStage = "prepare"
						}
						run(step)
						for _, name := range []string{"qc_frontier_dst", "qc_frontier_table", "qc_frontier_view"} {
							fixture.QueryTableCreateAbsent(t, run, name)
						}
						run(fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}
