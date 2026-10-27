package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func explainDMLSourceReturning(t *testing.T) {
	cases := []struct {
		name, sql            string
		returning, auxiliary []string
	}{
		{"insert-alias", "INSERT INTO explain_dml AS x VALUES(3,30) RETURNING x.id", []string{"x.id"}, nil},
		{"insert-select-alias", "INSERT INTO explain_dml AS x SELECT s.id,s.v FROM explain_source s RETURNING x.id,x.v", []string{"x.id", "x.v"}, []string{"s.id", "s.v"}},
		{"insert-conflict-alias", "INSERT INTO explain_dml AS x VALUES(1,30) ON CONFLICT(id) DO UPDATE SET v=x.v+excluded.v RETURNING x.id,x.v", []string{"x.id", "x.v"}, []string{"x.v", "excluded.v"}},
		{"update-derived", "UPDATE explain_dml t SET v=s.delta FROM (SELECT id,v AS delta FROM explain_source) s WHERE t.id=s.id RETURNING s.delta,old.v,t.v", []string{"s.delta", "old.v", "t.v"}, nil},
		{"update-values", "UPDATE explain_dml t SET v=s.delta FROM (VALUES(1,91)) s(id,delta) WHERE t.id=s.id RETURNING s.delta", []string{"s.delta"}, nil},
		{"update-physical-alias", "UPDATE explain_dml t SET v=s.v FROM explain_source s WHERE t.id=s.id RETURNING s.v", []string{"s.v"}, nil},
		{"update-correlated", "UPDATE explain_dml t SET v=s.delta FROM (VALUES(1,91)) s(id,delta) WHERE t.id=s.id RETURNING (SELECT s.delta)", []string{"SubPlan"}, []string{"Output: s.delta"}},
		{"delete-values", "DELETE FROM explain_dml t USING (VALUES(1,91)) s(id,delta) WHERE t.id=s.id RETURNING s.delta", []string{"s.delta"}, nil},
		{"delete-derived", "DELETE FROM explain_dml t USING (SELECT id,v AS delta FROM explain_source) s WHERE t.id=s.id RETURNING s.delta", []string{"s.delta"}, nil},
		{"delete-correlated", "DELETE FROM explain_dml t USING (VALUES(1,91)) s(id,delta) WHERE t.id=s.id RETURNING (SELECT s.delta)", []string{"SubPlan"}, []string{"Output: s.delta"}},
		{"merge-column", "MERGE INTO explain_dml t USING (VALUES(1,91)) s(id,delta) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.delta RETURNING s.delta", []string{"s.delta"}, nil},
		{"merge-whole-row", "MERGE INTO explain_dml t USING (VALUES(1,91)) s(id,delta) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.delta RETURNING s", []string{"s.id", "s.delta"}, nil},
		{"merge-correlated", "MERGE INTO explain_dml t USING (VALUES(1,91)) s(id,delta) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.delta RETURNING (SELECT s.delta)", []string{"SubPlan"}, []string{"Output: s.delta"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE explain_dml(id integer PRIMARY KEY,v integer)", 0))
					run(fixture.Exec("CREATE TABLE explain_source(id integer,v integer)", 0))
					run(fixture.Exec("INSERT INTO explain_dml VALUES(1,10),(2,20)", 2))
					run(fixture.Exec("INSERT INTO explain_source VALUES(1,11),(4,44)", 2))
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
							lines := explainQuery(t, p, b, "EXPLAIN (VERBOSE) "+tc.sql, prepared)
							if b.PG {
								// The oracle chooses its own methods and may inline a
								// source/subplan. Only successful planning and target
								// identity are required, never native text parity.
								explainContains(t, lines, "explain_dml")
							} else {
								var returning string
								for _, line := range lines {
									if strings.HasPrefix(strings.TrimSpace(line), "RETURNING:") {
										returning = line
										break
									}
								}
								if returning == "" {
									t.Fatalf("missing RETURNING detail: %v", lines)
								}
								explainContains(t, []string{returning}, tc.returning...)
								explainContains(t, lines, tc.auxiliary...)
							}
							run(fixture.Select("SELECT id,v FROM explain_dml ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
							run(fixture.Select("SELECT id,v FROM explain_source ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(4), int64(44)}}))
						})
					}
				})
			}
		})
	}
}
