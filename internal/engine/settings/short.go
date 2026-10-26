package settings

// Short-option counterparts are ordinary SQL settings, not command-line
// options or consumers of physical planner, logging, or durability mechanisms.
var shortBuiltins = []Definition{
	{Name: "enable_bitmapscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of bitmap-scan plans."},
	{Name: "enable_hashjoin", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of hash join plans."},
	{Name: "enable_indexscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of index-scan plans."},
	{Name: "enable_mergejoin", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of merge join plans."},
	{Name: "enable_nestloop", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of nested-loop join plans."},
	{Name: "enable_indexonlyscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of index-only-scan plans."},
	{Name: "enable_seqscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of sequential-scan plans."},
	{Name: "enable_tidscan", Type: Boolean, BootDefault: "on", Category: "Query Tuning / Planner Method Configuration", Description: "Enables the planner's use of TID scan plans."},
	{Name: "log_parser_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Statistics / Monitoring", Description: "Writes parser performance statistics to the server log."},
	{Name: "log_planner_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Statistics / Monitoring", Description: "Writes planner performance statistics to the server log."},
	{Name: "log_executor_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Statistics / Monitoring", Description: "Writes executor performance statistics to the server log."},
}
