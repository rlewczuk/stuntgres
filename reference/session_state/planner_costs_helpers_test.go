package reference_test

import (
	"fmt"
	"strings"

	"stuntgres/reference/fixture"
)

// Independent released PostgreSQL expectations; never native descriptor output.
type plannerCostSetting struct { name, kind, unit, boot, show, min, max, description, extra string }
var plannerCostSettings = []plannerCostSetting{
	{"cpu_index_tuple_cost", "real", "", "0.005", "0.005", "0", "1.79769e+308", "Sets the planner's estimate of the cost of processing each index entry during an index scan.", ""},
	{"cpu_operator_cost", "real", "", "0.0025", "0.0025", "0", "1.79769e+308", "Sets the planner's estimate of the cost of processing each operator or function call.", ""},
	{"cpu_tuple_cost", "real", "", "0.01", "0.01", "0", "1.79769e+308", "Sets the planner's estimate of the cost of processing each tuple (row).", ""},
	{"effective_cache_size", "integer", "8kB", "524288", "4GB", "1", "2147483647", "Sets the planner's assumption about the total size of the data caches.", "That is, the total size of the caches (kernel cache and shared buffers) used for PostgreSQL data files. This is measured in disk pages, which are normally 8 kB each."},
	{"jit_above_cost", "real", "", "100000", "100000", "-1", "1.79769e+308", "Perform JIT compilation if query is more expensive.", "-1 disables JIT compilation."},
	{"jit_inline_above_cost", "real", "", "500000", "500000", "-1", "1.79769e+308", "Perform JIT inlining if query is more expensive.", "-1 disables inlining."},
	{"jit_optimize_above_cost", "real", "", "500000", "500000", "-1", "1.79769e+308", "Optimize JIT-compiled functions if query is more expensive.", "-1 disables optimization."},
	{"min_parallel_index_scan_size", "integer", "8kB", "64", "512kB", "0", "715827882", "Sets the minimum amount of index data for a parallel scan.", "If the planner estimates that it will read a number of index pages too small to reach this limit, a parallel scan will not be considered."},
	{"min_parallel_table_scan_size", "integer", "8kB", "1024", "8MB", "0", "715827882", "Sets the minimum amount of table data for a parallel scan.", "If the planner estimates that it will read a number of table pages too small to reach this limit, a parallel scan will not be considered."},
	{"parallel_setup_cost", "real", "", "1000", "1000", "0", "1.79769e+308", "Sets the planner's estimate of the cost of starting up worker processes for parallel query.", ""},
	{"parallel_tuple_cost", "real", "", "0.1", "0.1", "0", "1.79769e+308", "Sets the planner's estimate of the cost of passing each tuple (row) from worker to leader backend.", ""},
	{"random_page_cost", "real", "", "4", "4", "0", "1.79769e+308", "Sets the planner's estimate of the cost of a nonsequentially fetched disk page.", ""},
	{"seq_page_cost", "real", "", "1", "1", "0", "1.79769e+308", "Sets the planner's estimate of the cost of a sequentially fetched disk page.", ""},
}

func plannerCostNames() string {
	names := make([]string, len(plannerCostSettings))
	for i, d := range plannerCostSettings { names[i] = physicalLiteral(d.name) }
	return strings.Join(names, ",")
}

func plannerCostBounds(d plannerCostSetting, value string) fixture.Diagnostic {
	unit := ""
	if d.unit != "" { unit = " " + d.unit }
	return physicalDiagnostic("22023", fmt.Sprintf("%s%s is outside the valid range for parameter %q (%s%s .. %s%s)", value, unit, d.name, d.min, unit, d.max, unit))
}

func (s *physicalCase) plannerCostValue(d plannerCostSetting, value, raw, source string, prepared bool) {
	s.resourceValue(d.name, value, raw, source, prepared)
}

func (s *physicalCase) plannerCostSet(name, value string, prepared bool) {
	step := fixture.Exec("SET "+name+"="+physicalLiteral(value), 0)
	step.Prepared = prepared
	s.run(step)
}
