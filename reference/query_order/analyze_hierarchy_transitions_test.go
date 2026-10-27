package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeHierarchyTransitions(t *testing.T) {
	for _, transition := range []string{"detach", "drop"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", transition, prepared), func(t *testing.T) {
				analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c := analyzeConn(t, p, b)
					run := func(s fixture.Step) { analyzeRun(t, p, b, c, s) }
					analyze := func() {
						s := fixture.Exec("ANALYZE analyze_transition_parent", 0)
						s.Prepared = prepared
						run(s)
					}
					run(fixture.Exec("CREATE TABLE analyze_transition_parent(a INT)", 0))
					run(fixture.Exec("CREATE TABLE analyze_transition_child() INHERITS(analyze_transition_parent)", 0))
					run(fixture.Exec("INSERT INTO analyze_transition_parent VALUES(1),(1)", 2))
					run(fixture.Exec("INSERT INTO analyze_transition_child VALUES(2),(2),(2)", 3))
					analyze()
					checkParent := func(children bool) {
						t.Helper()
						analyzeTuples(t, p, b, c, "analyze_transition_parent", 2)
						analyzeWantEstimate(t, p, c, "ONLY analyze_transition_parent", "", 2)
						analyzeWantEstimate(t, p, c, "ONLY analyze_transition_parent", "a=1", 2)
						wantRows := analyzeCountRows(2, int64(1))
						wantEstimate := int64(2)
						if children {
							wantRows = append(wantRows, analyzeCountRows(3, int64(2))...)
							wantEstimate = 5
						}
						analyzeWantEstimate(t, p, c, "analyze_transition_parent", "", wantEstimate)
						for _, route := range []bool{false, true} {
							s := fixture.Select("SELECT a FROM analyze_transition_parent ORDER BY a", []string{"a"}, []string{"INT4"}, wantRows)
							s.Prepared = route
							run(s)
							s = fixture.Select("SELECT a FROM ONLY analyze_transition_parent ORDER BY a", []string{"a"}, []string{"INT4"}, analyzeCountRows(2, int64(1)))
							s.Prepared = route
							run(s)
						}
						if !children {
							analyzeWantEstimate(t, p, c, "analyze_transition_parent", "a=1", 2)
							absent := int64(0)
							if b.PG {
								absent = 1
							}
							analyzeWantEstimate(t, p, c, "analyze_transition_parent", "a=2", absent)
						}
					}
					change := "ALTER TABLE analyze_transition_child NO INHERIT analyze_transition_parent"
					if transition == "drop" {
						change = "DROP TABLE analyze_transition_child"
					}
					checkParent(true)
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec(change, 0))
					analyze()
					checkParent(false)
					run(fixture.Exec("ROLLBACK", 0))
					// The edge and private distributions return before another scan.
					checkParent(true)
					// A fresh collection must also use the restored authority.
					analyze()
					checkParent(true)
					run(fixture.Exec(change, 0))
					analyze()
					checkParent(false)
					if transition == "detach" {
						run(fixture.Select("SELECT a FROM analyze_transition_child ORDER BY a", []string{"a"}, []string{"INT4"}, analyzeCountRows(3, int64(2))))
					}
				})
			})
		}
	}
}
