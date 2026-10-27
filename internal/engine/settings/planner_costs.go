package settings

import "math"

const plannerCostCategory = "Query Tuning / Planner Cost Constants"

// These descriptors share the ordinary USERSET parser and transaction journal.
var plannerCostBuiltins = []Definition{
	{Name: "seq_page_cost", Type: Floating, BootDefault: "1", FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Sets the planner's estimate of the cost of a sequentially fetched disk page."},
	{Name: "random_page_cost", Type: Floating, BootDefault: "4", FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Sets the planner's estimate of the cost of a nonsequentially fetched disk page."},
	{Name: "cpu_tuple_cost", Type: Floating, BootDefault: "0.01", FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Sets the planner's estimate of the cost of processing each tuple (row)."},
	{Name: "cpu_index_tuple_cost", Type: Floating, BootDefault: "0.005", FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Sets the planner's estimate of the cost of processing each index entry during an index scan."},
	{Name: "cpu_operator_cost", Type: Floating, BootDefault: "0.0025", FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Sets the planner's estimate of the cost of processing each operator or function call."},
	{Name: "parallel_setup_cost", Type: Floating, BootDefault: "1000", FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Sets the planner's estimate of the cost of starting up worker processes for parallel query."},
	{Name: "parallel_tuple_cost", Type: Floating, BootDefault: "0.1", FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Sets the planner's estimate of the cost of passing each tuple (row) from worker to leader backend."},
	{Name: "min_parallel_table_scan_size", Type: Integer, Unit: Blocks8kB, BootDefault: "1024", IntegerMax: 715827882, Category: plannerCostCategory, Description: "Sets the minimum amount of table data for a parallel scan.", ExtraDescription: "If the planner estimates that it will read a number of table pages too small to reach this limit, a parallel scan will not be considered."},
	{Name: "min_parallel_index_scan_size", Type: Integer, Unit: Blocks8kB, BootDefault: "64", IntegerMax: 715827882, Category: plannerCostCategory, Description: "Sets the minimum amount of index data for a parallel scan.", ExtraDescription: "If the planner estimates that it will read a number of index pages too small to reach this limit, a parallel scan will not be considered."},
	{Name: "effective_cache_size", Type: Integer, Unit: Blocks8kB, BootDefault: "524288", IntegerMin: 1, IntegerMax: 2147483647, Category: plannerCostCategory, Description: "Sets the planner's assumption about the total size of the data caches.", ExtraDescription: "That is, the total size of the caches (kernel cache and shared buffers) used for PostgreSQL data files. This is measured in disk pages, which are normally 8 kB each."},
	{Name: "jit_above_cost", Type: Floating, BootDefault: "100000", FloatMin: -1, FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Perform JIT compilation if query is more expensive.", ExtraDescription: "-1 disables JIT compilation."},
	{Name: "jit_inline_above_cost", Type: Floating, BootDefault: "500000", FloatMin: -1, FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Perform JIT inlining if query is more expensive.", ExtraDescription: "-1 disables inlining."},
	{Name: "jit_optimize_above_cost", Type: Floating, BootDefault: "500000", FloatMin: -1, FloatMax: math.MaxFloat64, Category: plannerCostCategory, Description: "Optimize JIT-compiled functions if query is more expensive.", ExtraDescription: "-1 disables optimization."},
}
