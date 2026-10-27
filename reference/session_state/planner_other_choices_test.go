package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func (s *physicalCase) plannerOtherExplain(query string, prepared bool) string {
	s.t.Helper()
	query = "EXPLAIN " + query
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query)
		if e != nil {
			s.t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, query)
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var text strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			s.t.Fatal(err)
		}
		text.WriteString(line)
		text.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		s.t.Fatal(err)
	}
	return text.String()
}

func TestReferencePlannerOtherStatistics(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE planner_other_stats(n integer)", 0))
		s.run(fixture.Exec("INSERT INTO planner_other_stats VALUES(1),(1),(1),(1),(2),(2),(2),(3),(3),(4)", 10))
		const query = "SELECT n FROM planner_other_stats WHERE n=2"
		check := func(want int) {
			for _, prepared := range []bool{false, true} {
				plan := s.plannerOtherExplain(query, prepared)
				if s.pg {
					if !strings.Contains(plan, "Scan on planner_other_stats") || !strings.Contains(plan, fmt.Sprintf("rows=%d width=", want)) {
						t.Fatal("wrong PostgreSQL distribution estimate", want, plan)
					}
				} else {
					found := false
					for _, line := range strings.Split(plan, "\n") {
						if strings.Contains(line, "Filter (estimated rows=") {
							found = true
							if !strings.Contains(line, fmt.Sprintf("estimated rows=%d logical", want)) {
								t.Fatal("wrong collected distribution estimate", want, plan)
							}
						}
					}
					if !found {
						t.Fatal(plan)
					}
				}
				step := fixture.Select(query, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(2)}})
				step.Prepared = prepared
				step.Bag = true
				s.run(step)
			}
		}
		s.plannerCostSet("default_statistics_target", "1", false)
		s.run(fixture.Exec("ANALYZE planner_other_stats", 0))
		check(2)
		s.plannerCostSet("default_statistics_target", "3", true)
		check(2)
		s.run(fixture.Exec("ANALYZE planner_other_stats", 0))
		check(3)
	})
}

func TestReferencePlannerOtherConstraintChoices(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		for _, q := range []string{"CREATE TABLE planner_other_check(n integer,s text,CHECK(n>0))", "CREATE TABLE planner_other_parent(n integer,s text)", "CREATE TABLE planner_other_child(CHECK(n>0)) INHERITS(planner_other_parent)", "CREATE TABLE planner_other_partition(n integer,s text) PARTITION BY RANGE(n)", "CREATE TABLE planner_other_positive PARTITION OF planner_other_partition FOR VALUES FROM(1) TO(10)"} {
			s.run(fixture.Exec(q, 0))
		}
		for _, name := range []string{"planner_other_check", "planner_other_child"} {
			s.run(fixture.Exec("INSERT INTO "+name+" VALUES(1,'a'),(2,'b'),(2,'b'),(NULL,NULL)", 4))
		}
		s.run(fixture.Exec("INSERT INTO planner_other_partition VALUES(1,'a'),(2,'b'),(2,'b')", 3))
		for _, mode := range []string{"partition", "on", "off"} {
			s.plannerCostSet("constraint_exclusion", mode, false)
			for _, name := range []string{"planner_other_check", "ONLY planner_other_check", "planner_other_parent", "ONLY planner_other_child", "planner_other_partition"} {
				for _, prepared := range []bool{false, true} {
					for _, tc := range []struct {
						predicate string
						rows      [][]any
					}{{"n<0", nil}, {"n=2", [][]any{{int64(2), "b"}, {int64(2), "b"}}}, {"n IS NULL", [][]any{{nil, nil}}}} {
						if name == "planner_other_partition" && tc.predicate == "n IS NULL" {
							tc.rows = nil
						}
						step := fixture.Select("SELECT n,s FROM "+name+" WHERE "+tc.predicate, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.rows)
						step.Prepared = prepared
						step.Bag = true
						s.run(step)
					}
					plan := s.plannerOtherExplain("SELECT n,s FROM "+name+" WHERE n<0", prepared)
					if name == "planner_other_check" {
						if s.pg {
							if mode == "on" && strings.Contains(plan, "Seq Scan on planner_other_check") || mode != "on" && !strings.Contains(plan, "Seq Scan on planner_other_check") {
								t.Fatal(mode, plan)
							}
						} else {
							want := "Pruned Sources: planner_other_check"
							if strings.Contains(plan, want) != (mode == "on") {
								t.Fatal(mode, plan)
							}
						}
					}
				}
			}
			query := "SELECT n,s FROM planner_other_check WHERE n=$1"
			held, err := s.conn.PrepareContext(s.p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			for _, next := range []string{"off", "on", mode} {
				s.plannerCostSet("constraint_exclusion", next, false)
				step := fixture.Select(query, []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}}, 2)
				step.Bag = true
				fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: query}, s.recorder, step)
			}
			held.Close()
		}
	})
}

func TestReferencePlannerOtherCursorChoices(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.plannerCostSeed()
		for _, fraction := range []string{"0", "0.0001", "1"} {
			s.plannerCostSet("cursor_tuple_fraction", fraction, false)
			for _, prepared := range []bool{false, true} {
				s.plannerCostExplain(true, prepared)
			}
			// Implicit WITH HOLD declaration exercises the eligible equality/hash
			// source before promotion. Its complete promoted result must survive
			// a later setting change, independently of the composed choice proof.
			s.run(fixture.Exec("DECLARE other_hold NO SCROLL CURSOR WITH HOLD FOR SELECT n,s FROM planner_cost_rows WHERE n=2", 0))
			s.plannerCostSet("cursor_tuple_fraction", "0.7", false)
			heldRows := fixture.Select("FETCH ALL FROM other_hold", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}})
			heldRows.Bag = true
			s.run(heldRows)
			s.run(fixture.Exec("CLOSE other_hold", 0))
			s.plannerCostSet("cursor_tuple_fraction", fraction, false)
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Exec("DECLARE other_cursor SCROLL CURSOR FOR SELECT n,s FROM planner_cost_rows", 0))
			s.plannerCostSet("cursor_tuple_fraction", "0.7", false)
			step := fixture.Select("FETCH ALL FROM other_cursor", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(1), "a"}, {int64(2), "b"}, {nil, nil}, {int64(3), "c"}})
			step.Bag = true
			s.run(step)
			s.run(fixture.Exec("CLOSE other_cursor", 0))
			s.run(fixture.Exec("COMMIT", 0))
		}
	})
}

func TestReferencePlannerOtherRecursiveEstimates(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		const query = "WITH RECURSIVE r(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r"
		for _, factor := range []string{"1", "10", "100"} {
			s.plannerCostSet("recursive_worktable_factor", factor, false)
			for _, prepared := range []bool{false, true} {
				step := fixture.Select(query, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
				step.Bag = true
				step.Prepared = prepared
				s.run(step)
				plan := s.plannerOtherExplain(query, prepared)
				label := "Worktable Scan"
				if s.pg {
					label = "WorkTable Scan"
				}
				found := false
				for _, line := range strings.Split(plan, "\n") {
					if strings.Contains(line, label) {
						found = true
						if s.pg {
							// Released PostgreSQL estimates seed1*factor working
							// rows, applies the default inequality selectivity1/3
							// for n<3, then clamps/rounds rows. This is distinct
							// from the native unfiltered logical worktable rows.
							filteredRows := "1"
							switch factor {
							case "10":
								filteredRows = "3"
							case "100":
								filteredRows = "33"
							}
							if !strings.Contains(line, "rows="+filteredRows+" width=") {
								t.Fatal("wrong PostgreSQL filtered worktable estimate", factor, line)
							}
						}
						if !s.pg && !strings.Contains(line, "rows="+factor+" logical") {
							t.Fatal(factor, line)
						}
					}
				}
				if !found {
					t.Fatal(plan)
				}
			}
		}
	})
}
