package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Release expectations, independent of the native registry.
var plannerSettings = []struct{ name, boot, description, extra string }{
	{"enable_async_append", "on", "Enables the planner's use of async append plans.", ""},
	{"enable_bitmapscan", "on", "Enables the planner's use of bitmap-scan plans.", ""},
	{"enable_distinct_reordering", "on", "Enables reordering of DISTINCT keys.", ""},
	{"enable_gathermerge", "on", "Enables the planner's use of gather merge plans.", ""},
	{"enable_group_by_reordering", "on", "Enables reordering of GROUP BY keys.", ""},
	{"enable_hashagg", "on", "Enables the planner's use of hashed aggregation plans.", ""},
	{"enable_hashjoin", "on", "Enables the planner's use of hash join plans.", ""},
	{"enable_incremental_sort", "on", "Enables the planner's use of incremental sort steps.", ""},
	{"enable_indexscan", "on", "Enables the planner's use of index-scan plans.", ""},
	{"enable_indexonlyscan", "on", "Enables the planner's use of index-only-scan plans.", ""},
	{"enable_material", "on", "Enables the planner's use of materialization.", ""},
	{"enable_memoize", "on", "Enables the planner's use of memoization.", ""},
	{"enable_mergejoin", "on", "Enables the planner's use of merge join plans.", ""},
	{"enable_nestloop", "on", "Enables the planner's use of nested-loop join plans.", ""},
	{"enable_parallel_append", "on", "Enables the planner's use of parallel append plans.", ""},
	{"enable_parallel_hash", "on", "Enables the planner's use of parallel hash plans.", ""},
	{"enable_partition_pruning", "on", "Enables plan-time and execution-time partition pruning.", "Allows the query planner and executor to compare partition bounds to conditions in the query to determine which partitions must be scanned."},
	{"enable_partitionwise_join", "off", "Enables partitionwise join.", ""},
	{"enable_partitionwise_aggregate", "off", "Enables partitionwise aggregation and grouping.", ""},
	{"enable_presorted_aggregate", "on", "Enables the planner's ability to produce plans that provide presorted input for ORDER BY / DISTINCT aggregate functions.", "Allows the query planner to build plans that provide presorted input for aggregate functions with an ORDER BY / DISTINCT clause.  When disabled, implicit sorts are always performed during execution."},
	{"enable_self_join_elimination", "on", "Enables removal of unique self-joins.", ""},
	{"enable_seqscan", "on", "Enables the planner's use of sequential-scan plans.", ""},
	{"enable_sort", "on", "Enables the planner's use of explicit sort steps.", ""},
	{"enable_tidscan", "on", "Enables the planner's use of TID scan plans.", ""},
}

func (s *physicalCase) plannerRead(name, value string, prepared bool) {
	s.t.Helper()
	for _, q := range []struct{ sql, column string }{{"SHOW " + name, name}, {"SELECT current_setting(" + physicalLiteral(name) + ")", "current_setting"}} {
		step := fixture.Select(q.sql, []string{q.column}, []string{"TEXT"}, [][]any{{value}})
		step.Prepared = prepared
		s.run(step)
	}
}

func TestReferencePlannerSettingsInventory(t *testing.T) {
	for _, d := range plannerSettings {
		t.Run(d.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				for _, prepared := range []bool{false, true} {
					s.plannerRead(d.name, d.boot, prepared)
					var extra any
					if d.extra != "" { extra = d.extra }
					step := fixture.Select("SELECT " + physicalColumnsSQL + " FROM pg_settings WHERE name=$1", physicalColumns, physicalTypes,
						[][]any{{d.name, d.boot, nil, "Query Tuning / Planner Method Configuration", d.description, extra, "user", "bool", "default", nil, nil, nil, d.boot, d.boot, nil, nil, false}}, d.name)
					step.Prepared = prepared
					s.run(step)
					rows, err := s.conn.QueryContext(s.p.Ctx, "SHOW ALL")
					if err != nil { s.t.Fatal(err) }
					count := 0
					for rows.Next() {
						var name, value, description string
						if err := rows.Scan(&name, &value, &description); err != nil { s.t.Fatal(err) }
						if name == d.name { count++; if value != d.boot || description != d.description { s.t.Fatal(name, value, description) } }
					}
					if err := rows.Err(); err != nil { s.t.Fatal(err) }
					if err := rows.Close(); err != nil { s.t.Fatal(err) }
					if count != 1 { s.t.Fatal(d.name, count) }
				}
			})
		})
	}
}

func TestReferencePlannerSettingsValidation(t *testing.T) {
	for _, d := range plannerSettings {
		t.Run(d.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				s.run(fixture.Exec("CREATE TABLE pm_sentinel(k integer)", 0))
				s.run(fixture.Exec("INSERT INTO pm_sentinel VALUES(7)", 1))
				for _, prepared := range []bool{false, true} {
					for _, tc := range []struct{ input, want string }{{"off","off"},{"TRUE","on"},{"f","off"},{"yes","on"},{"NO","off"},{"1","on"},{"0","off"},{"on","on"}} {
						s.developerExec("SET " + d.name + "=" + physicalLiteral(tc.input), prepared)
						s.plannerRead(d.name, tc.want, prepared)
					}
					for _, invalid := range []string{"o","bogus",""," true ","2"} {
						diagnostic := physicalDiagnostic("22023", fmt.Sprintf("parameter %q requires a Boolean value", d.name))
						s.run(physicalError("SET " + d.name + "=" + physicalLiteral(invalid), diagnostic, prepared, false))
						s.run(physicalError("SELECT set_config($1,$2,$3)", diagnostic, prepared, true, d.name, invalid, false))
						s.plannerRead(d.name, "on", prepared)
						s.plannerRead("enable_async_append", "on", prepared)
					}
					s.run(physicalError("SET " + d.name + " TO on,off", physicalDiagnostic("22023", "SET " + d.name + " takes only one argument"), prepared, false))
					step := fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"off"}}, d.name, "off", false)
					step.Prepared = prepared; s.run(step)
					s.plannerRead(d.name, "off", prepared)
					for _, q := range []string{"RESET " + d.name, "SET " + d.name + "=DEFAULT", "RESET ALL"} {
						s.developerExec("SET " + d.name + "=off", prepared)
						s.developerExec(q, prepared)
						s.plannerRead(d.name, d.boot, prepared)
					}
					s.run(fixture.Select("SELECT k FROM pm_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			})
		})
	}
}

func TestReferencePlannerSettingsPermissions(t *testing.T) {
	physicalCases(t, []string{"pm_login"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE pm_login LOGIN PASSWORD 'role-core-fixture'", 0))
		ordinary := s.login("pm_login")
		for _, d := range plannerSettings {
			for _, prepared := range []bool{false, true} {
				ordinary.plannerRead(d.name, d.boot, prepared)
				ordinary.developerExec("SET " + d.name + "=off", prepared)
				ordinary.plannerRead(d.name, "off", prepared)
				ordinary.run(physicalError("SET " + d.name + "='bogus'", physicalDiagnostic("22023", fmt.Sprintf("parameter %q requires a Boolean value", d.name)), prepared, false))
				ordinary.developerExec("RESET " + d.name, prepared)
				ordinary.plannerRead(d.name, d.boot, prepared)
				step := fixture.Select("SELECT name FROM pg_settings WHERE name=$1", []string{"name"}, []string{"TEXT"}, [][]any{{d.name}}, d.name)
				step.Prepared = prepared; ordinary.run(step)
			}
		}
		rows,err:=ordinary.conn.QueryContext(s.p.Ctx,"SHOW ALL")
		if err!=nil { s.t.Fatal(err) }
		counts:=map[string]int{}
		for rows.Next() {
			var name,value,description string
			if err:=rows.Scan(&name,&value,&description); err!=nil { s.t.Fatal(err) }
			for _, d:=range plannerSettings {
				if name==d.name {
					counts[name]++
					if value!=d.boot || description!=d.description { s.t.Fatal(name,value,description) }
				}
			}
		}
		if err:=rows.Err(); err!=nil { s.t.Fatal(err) }; rows.Close()
		for _, d:=range plannerSettings { if counts[d.name]!=1 { s.t.Fatal(d.name,counts[d.name]) } }
	})
}
