package settings

import "stuntgres/internal/sqlstate"

const plannerOtherCategory = "Query Tuning / Other Planner Options"

var plannerOtherBuiltins = []Definition{
	{Name: "default_statistics_target", Type: Integer, BootDefault: "100", IntegerMin: 1, IntegerMax: 10000, Category: plannerOtherCategory, Description: "Sets the default statistics target.", ExtraDescription: "This applies to table columns that have not had a column-specific target set via ALTER TABLE SET STATISTICS."},
	{Name: "constraint_exclusion", Type: Enum, BootDefault: "partition", EnumValues: []string{"partition", "on", "off"}, Normalize: normalizeConstraintExclusion, Category: plannerOtherCategory, Description: "Enables the planner to use constraints to optimize queries.", ExtraDescription: "Table scans will be skipped if their constraints guarantee that no rows match the query."},
	{Name: "cursor_tuple_fraction", Type: Floating, BootDefault: "0.1", FloatMax: 1, Category: plannerOtherCategory, Description: "Sets the planner's estimate of the fraction of a cursor's rows that will be retrieved."},
	{Name: "from_collapse_limit", Type: Integer, BootDefault: "8", IntegerMin: 1, IntegerMax: 2147483647, Category: plannerOtherCategory, Description: "Sets the FROM-list size beyond which subqueries are not collapsed.", ExtraDescription: "The planner will merge subqueries into upper queries if the resulting FROM list would have no more than this many items."},
	{Name: "jit", Type: Boolean, BootDefault: "on", Category: plannerOtherCategory, Description: "Allow JIT compilation."},
	{Name: "join_collapse_limit", Type: Integer, BootDefault: "8", IntegerMin: 1, IntegerMax: 2147483647, Category: plannerOtherCategory, Description: "Sets the FROM-list size beyond which JOIN constructs are not flattened.", ExtraDescription: "The planner will flatten explicit JOIN constructs into lists of FROM items whenever a list of no more than this many items would result."},
	{Name: "plan_cache_mode", Type: Enum, BootDefault: "auto", EnumValues: []string{"auto", "force_generic_plan", "force_custom_plan"}, Category: plannerOtherCategory, Description: "Controls the planner's selection of custom or generic plan.", ExtraDescription: "Prepared statements can have custom and generic plans, and the planner will attempt to choose which is better.  This can be set to override the default behavior."},
	{Name: "recursive_worktable_factor", Type: Floating, BootDefault: "10", FloatMin: .001, FloatMax: 1000000, Category: plannerOtherCategory, Description: "Sets the planner's estimate of the average size of a recursive query's working table."},
}

func normalizeConstraintExclusion(input string) (string, *sqlstate.Error, error) {
	switch FoldName(input) {
	case "partition":
		return "partition", nil, nil
	case "on", "true", "yes", "1":
		return "on", nil, nil
	case "off", "false", "no", "0":
		return "off", nil, nil
	}
	return "", nil, (Definition{Name: "constraint_exclusion"}).invalid(input, "Available values: partition, on, off.")
}
