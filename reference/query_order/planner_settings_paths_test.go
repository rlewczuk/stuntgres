package reference_test

import (
	"strings"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferencePlannerSettingsIndexChoice(t *testing.T) {
	plannerQueryCases(t,func(s *plannerQueryCase) {
		s.exec("CREATE TABLE pm_index(k integer,v text)")
		s.run(fixture.Exec("INSERT INTO pm_index VALUES(1,'a'),(1,'b'),(2,'c'),(NULL,'n')",4))
		s.exec("CREATE INDEX pm_hash ON pm_index USING hash(k)")
		q := "SELECT k,v FROM pm_index WHERE k=1"
		want := [][]any{{int64(1),"a"},{int64(1),"b"}}
		s.toggles("enable_indexscan",q,[]string{"k","v"},[]string{"INT4","TEXT"},want,true)
		for _, value := range []string{"on","off"} {
			s.exec("SET enable_indexscan="+value)
			s.run(fixture.Prepared(fixture.Select("SELECT v FROM pm_index WHERE k=$1 ORDER BY v",[]string{"v"},[]string{"TEXT"},[][]any{{"a"},{"b"}},int64(1))))
			s.run(fixture.Prepared(fixture.Select("SELECT v FROM pm_index WHERE k=$1",[]string{"v"},[]string{"TEXT"},nil,nil)))
			s.exec("BEGIN")
			s.run(fixture.Exec("INSERT INTO pm_index VALUES(1,'private')",1))
			s.run(fixture.Select("SELECT v FROM pm_index WHERE k=1 ORDER BY v FOR UPDATE",[]string{"v"},[]string{"TEXT"},[][]any{{"a"},{"b"},{"private"}}))
			s.exec("ROLLBACK")
		}
	})
}
func TestReferencePlannerSettingsPruningChoice(t *testing.T) {
	plannerQueryCases(t, func(s *plannerQueryCase) {
		for _, q := range []string{
			"CREATE TABLE pm_parts(k integer) PARTITION BY RANGE(k)",
			"CREATE TABLE pm_p0 PARTITION OF pm_parts FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE pm_p1 PARTITION OF pm_parts FOR VALUES FROM(10) TO(20)",
			"CREATE TABLE pm_check(k integer,v text,CHECK(k>0))",
			"CREATE TABLE pm_parent(k integer,v text)",
			"CREATE TABLE pm_child(CHECK(k>0)) INHERITS(pm_parent)",
			"CREATE TABLE pm_nn(k integer NOT NULL,v text)",
			"CREATE TABLE pm_nn_parent(k integer,v text)",
			"CREATE TABLE pm_nn_child(k integer NOT NULL) INHERITS(pm_nn_parent)",
		} {
			s.exec(q)
		}
		s.run(fixture.Exec("INSERT INTO pm_parts VALUES(1),(11)", 2))
		for _, name := range []string{"pm_check", "pm_child"} {
			s.run(fixture.Exec("INSERT INTO "+name+" VALUES(1,'a'),(2,'b'),(2,'b'),(NULL,NULL)", 4))
		}
		for _, name := range []string{"pm_nn", "pm_nn_child"} {
			s.run(fixture.Exec("INSERT INTO "+name+" VALUES(1,'a'),(2,'b'),(2,'b')", 3))
		}
		s.run(fixture.Exec("INSERT INTO pm_parent VALUES(-1,'parent')", 1))
		s.run(fixture.Exec("INSERT INTO pm_nn_parent VALUES(NULL,'parent-null')", 1))

		// These handles predate every permission change; each execution must
		// admit both current permissions rather than retain prepare-time values.
		rangeSQL := "SELECT k FROM pm_parts WHERE k=$1"
		rangeStmt, err := s.conn.PrepareContext(s.p.Ctx, rangeSQL)
		if err != nil {
			s.t.Fatal(err)
		}
		defer rangeStmt.Close()
		type constraintScan struct {
			from, excluded string
			inherited, notNull bool
		}
		scans := []constraintScan{
			{"pm_check", "pm_check", false, false},
			{"ONLY pm_check", "pm_check", false, false},
			{"pm_child", "pm_child", false, false},
			{"ONLY pm_child", "pm_child", false, false},
			{"pm_parent", "pm_child", true, false},
			{"pm_nn", "pm_nn", false, true},
			{"ONLY pm_nn", "pm_nn", false, true},
			{"pm_nn_child", "pm_nn_child", false, true},
			{"ONLY pm_nn_child", "pm_nn_child", false, true},
			{"pm_nn_parent", "pm_nn_child", true, true},
		}
		held := make([]fixture.HeldStatement, len(scans))
		for i, scan := range scans {
			q := "SELECT k,v FROM "+scan.from+" WHERE k=$1"
			stmt, err := s.conn.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			defer stmt.Close()
			held[i] = fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: q}
		}
		// PostgreSQL's physical plan vocabulary is deliberately not a native
		// logical-plan golden. Public bags below are identical on both backends.
		explain := func(q string, excluded []string, prepared bool) {
			s.t.Helper()
			if s.b.PG {
				return
			}
			query := "EXPLAIN "+q
			var executor fixture.Executor = s.conn
			if prepared {
				stmt, err := s.conn.PrepareContext(s.p.Ctx, query)
				if err != nil {
					s.t.Fatal(err)
				}
				defer stmt.Close()
				executor = fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: query}
			}
			rows, err := executor.QueryContext(s.p.Ctx, query)
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					s.t.Fatal(err)
				}
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "Pruned Sources: ") {
					details = append(details, strings.TrimPrefix(line, "Pruned Sources: "))
				}
			}
			if err := rows.Err(); err != nil {
				s.t.Fatal(err)
			}
			if strings.Join(details, "|") != strings.Join(excluded, "|") {
				s.t.Fatalf("%s: pruned=%q want=%q", q, details, excluded)
			}
		}
		for _, pruning := range []string{"off", "on"} {
			s.exec("SET enable_partition_pruning="+pruning)
			for _, mode := range []string{"on", "partition", "off"} {
				s.exec("SET constraint_exclusion="+mode)
				s.run(fixture.Select("SELECT k FROM pm_parts WHERE k=1", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				var excluded []string
				if pruning == "on" {
					excluded = []string{"pm_p1"}
				}
				for _, prepared := range []bool{false, true} {
					explain("SELECT k FROM pm_parts WHERE k=1", excluded, prepared)
				}
				for _, k := range []int64{1, 11} {
					fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: rangeStmt, SQL: rangeSQL}, s.b.Recorder, fixture.Select(rangeSQL, []string{"k"}, []string{"INT4"}, [][]any{{k}}, k))
				}
				for i, scan := range scans {
					allowed := mode == "on" || mode == "partition" && scan.inherited
					for _, tc := range []struct {
						predicate string
						rows [][]any
						exclude bool
					}{
						{"k<0", nil, !scan.notNull && allowed},
						{"k=2", [][]any{{int64(2), "b"}, {int64(2), "b"}}, false},
						{"k IS NULL", nil, scan.notNull && allowed},
						{"false", nil, true},
					} {
						if tc.predicate == "k<0" && scan.from == "pm_parent" {
							tc.rows = [][]any{{int64(-1), "parent"}}
						}
						if tc.predicate == "k IS NULL" && !scan.notNull {
							tc.rows = [][]any{{nil, nil}}
						}
						if tc.predicate == "k IS NULL" && scan.from == "pm_nn_parent" {
							tc.rows = [][]any{{nil, "parent-null"}}
						}
						q := "SELECT k,v FROM "+scan.from+" WHERE "+tc.predicate
						var names []string
						if tc.exclude {
							name := scan.excluded
							if tc.predicate == "false" && scan.inherited {
								name = scan.from+", "+scan.excluded
							}
							names = []string{name}
						}
						for _, prepared := range []bool{false, true} {
							step := fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, tc.rows)
							step.Prepared, step.Bag = prepared, true
							s.run(step)
							explain(q, names, prepared)
						}
					}
					for _, k := range []any{int64(-1), int64(2), nil} {
						var want [][]any
						if k == int64(2) {
							want = [][]any{{int64(2), "b"}, {int64(2), "b"}}
						} else if k == int64(-1) && scan.from == "pm_parent" {
							want = [][]any{{int64(-1), "parent"}}
						}
						step := fixture.Select(held[i].SQL, []string{"k", "v"}, []string{"INT4", "TEXT"}, want, k)
						step.Bag = true
						fixture.RunStep(s.t, s.p.Ctx, held[i], s.b.Recorder, step)
					}
				}
			}
		}
	})
}
func TestReferencePlannerSettingsGatherChoice(t *testing.T) {
	plannerQueryCases(t,func(s *plannerQueryCase) {
		s.exec("CREATE TABLE pm_gather(k integer)")
		s.run(fixture.Exec("INSERT INTO pm_gather VALUES(3),(NULL),(1),(2),(1)",5))
		s.exec("SET max_parallel_workers_per_gather=2")
		s.toggles("enable_gathermerge","SELECT k FROM pm_gather ORDER BY k NULLS LAST",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(1)},{int64(2)},{int64(3)},{nil}},false)
		s.toggles("enable_gathermerge","SELECT k FROM pm_gather",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(1)},{int64(2)},{int64(3)},{nil}},true)
	})
}
func TestReferencePlannerSettingsBoundaries(t *testing.T) {
	plannerQueryCases(t,func(s *plannerQueryCase) {
		s.tables()
		for _, name := range []string{"enable_bitmapscan","enable_distinct_reordering","enable_group_by_reordering","enable_incremental_sort","enable_indexonlyscan","enable_material","enable_memoize","enable_mergejoin","enable_nestloop","enable_parallel_append","enable_parallel_hash","enable_partitionwise_join","enable_partitionwise_aggregate","enable_presorted_aggregate","enable_self_join_elimination","enable_seqscan","enable_sort","enable_tidscan"} {
			for _, value := range []string{"on","off"} {
				s.exec("SET "+name+"="+value)
				q := "WITH x AS MATERIALIZED (SELECT k,v FROM pm_left) SELECT a.k,count(*),string_agg(DISTINCT a.v,',' ORDER BY a.v) FROM x a JOIN pm_left b ON a.k=b.k GROUP BY a.k ORDER BY a.k"
				s.run(fixture.Select(q,[]string{"k","count","string_agg"},[]string{"INT4","INT8","TEXT"},[][]any{{int64(1),int64(4),"a,b"},{int64(2),int64(1),"c"}}))
			}
		}
	})
}
