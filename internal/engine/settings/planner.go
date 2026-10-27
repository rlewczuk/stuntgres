package settings

// Planner method values share the ordinary transactional session store. Execution
// consumes only methods for which a real selector exists.
var plannerBuiltins = []Definition{
	{Name: "enable_async_append", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of async append plans."},
	{Name: "enable_bitmapscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of bitmap-scan plans."},
	{Name: "enable_distinct_reordering", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables reordering of DISTINCT keys."},
	{Name: "enable_gathermerge", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of gather merge plans."},
	{Name: "enable_group_by_reordering", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables reordering of GROUP BY keys."},
	{Name: "enable_hashagg", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of hashed aggregation plans."},
	{Name: "enable_hashjoin", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of hash join plans."},
	{Name: "enable_incremental_sort", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of incremental sort steps."},
	{Name: "enable_indexscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of index-scan plans."},
	{Name: "enable_indexonlyscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of index-only-scan plans."},
	{Name: "enable_material", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of materialization."},
	{Name: "enable_memoize", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of memoization."},
	{Name: "enable_mergejoin", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of merge join plans."},
	{Name: "enable_nestloop", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of nested-loop join plans."},
	{Name: "enable_parallel_append", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of parallel append plans."},
	{Name: "enable_parallel_hash", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of parallel hash plans."},
	{Name: "enable_partition_pruning", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables plan-time and execution-time partition pruning.", ExtraDescription: "Allows the query planner and executor to compare partition bounds to conditions in the query to determine which partitions must be scanned."},
	{Name: "enable_partitionwise_join", Type: Boolean, BootDefault: "off", Category: "Query Tuning / Planner Method Configuration", Description: "Enables partitionwise join."},
	{Name: "enable_partitionwise_aggregate", Type: Boolean, BootDefault: "off", Category: "Query Tuning / Planner Method Configuration", Description: "Enables partitionwise aggregation and grouping."},
	{Name: "enable_presorted_aggregate", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's ability to produce plans that provide presorted input for ORDER BY / DISTINCT aggregate functions.", ExtraDescription: "Allows the query planner to build plans that provide presorted input for aggregate functions with an ORDER BY / DISTINCT clause.  When disabled, implicit sorts are always performed during execution."},
	{Name: "enable_self_join_elimination", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables removal of unique self-joins."},
	{Name: "enable_seqscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of sequential-scan plans."},
	{Name: "enable_sort", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of explicit sort steps."},
	{Name: "enable_tidscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of TID scan plans."},
}
