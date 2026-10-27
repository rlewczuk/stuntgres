package settings

import "testing"

func TestPlannerSettingsDefinitions(t *testing.T) {
	cases := []struct{ name, boot, description string }{
		{"enable_async_append", "on", "Enables the planner's use of async append plans."},
		{"enable_bitmapscan", "on", "Enables the planner's use of bitmap-scan plans."},
		{"enable_distinct_reordering", "on", "Enables reordering of DISTINCT keys."},
		{"enable_gathermerge", "on", "Enables the planner's use of gather merge plans."},
		{"enable_group_by_reordering", "on", "Enables reordering of GROUP BY keys."},
		{"enable_hashagg", "on", "Enables the planner's use of hashed aggregation plans."},
		{"enable_hashjoin", "on", "Enables the planner's use of hash join plans."},
		{"enable_incremental_sort", "on", "Enables the planner's use of incremental sort steps."},
		{"enable_indexscan", "on", "Enables the planner's use of index-scan plans."},
		{"enable_indexonlyscan", "on", "Enables the planner's use of index-only-scan plans."},
		{"enable_material", "on", "Enables the planner's use of materialization."},
		{"enable_memoize", "on", "Enables the planner's use of memoization."},
		{"enable_mergejoin", "on", "Enables the planner's use of merge join plans."},
		{"enable_nestloop", "on", "Enables the planner's use of nested-loop join plans."},
		{"enable_parallel_append", "on", "Enables the planner's use of parallel append plans."},
		{"enable_parallel_hash", "on", "Enables the planner's use of parallel hash plans."},
		{"enable_partition_pruning", "on", "Enables plan-time and execution-time partition pruning."},
		{"enable_partitionwise_join", "off", "Enables partitionwise join."},
		{"enable_partitionwise_aggregate", "off", "Enables partitionwise aggregation and grouping."},
		{"enable_presorted_aggregate", "on", "Enables the planner's ability to produce plans that provide presorted input for ORDER BY / DISTINCT aggregate functions."},
		{"enable_self_join_elimination", "on", "Enables removal of unique self-joins."},
		{"enable_seqscan", "on", "Enables the planner's use of sequential-scan plans."},
		{"enable_sort", "on", "Enables the planner's use of explicit sort steps."},
		{"enable_tidscan", "on", "Enables the planner's use of TID scan plans."},
	}
	r := NewRegistry(nil, false)
	for _, tc := range cases {
		d, err := r.Lookup(tc.name)
		if err != nil { t.Fatal(err) }
		extra := ""
		switch tc.name {
		case "enable_partition_pruning": extra = "Allows the query planner and executor to compare partition bounds to conditions in the query to determine which partitions must be scanned."
		case "enable_presorted_aggregate": extra = "Allows the query planner to build plans that provide presorted input for aggregate functions with an ORDER BY / DISTINCT clause.  When disabled, implicit sorts are always performed during execution."
		}
		if d.Name != tc.name || d.BootDefault != tc.boot || d.Description != tc.description || d.ExtraDescription != extra || d.Type != Boolean || d.Access != Session || d.Category != "Query Tuning / Planner Method Configuration" || d.ReadRestricted || d.Unit != UnitNone || len(d.EnumValues) != 0 || d.IntegerMin != 0 || d.IntegerMax != 0 || d.FloatMin != 0 || d.FloatMax != 0 { t.Fatalf("%s: %+v", tc.name, d) }
		count := 0
		for _, name := range r.Order() { if name == tc.name { count++ } }
		if count != 1 { t.Fatal(tc.name, count) }
		if _, ok := r.Definitions()[tc.name]; !ok { t.Fatal(tc.name) }
	}
}
