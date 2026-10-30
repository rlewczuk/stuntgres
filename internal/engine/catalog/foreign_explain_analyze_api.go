package catalog

import "context"

// ForeignExplainOptions describe plain TEXT EXPLAIN. No execution state or
// transaction is available: these callbacks must not execute the statement.
type ForeignExplainOptions struct {
	Verbose, Costs, Analyze bool
}

// ForeignExplainProperty is an ordered, single-line provider plan property.
// Private plans are immutable provider values, never formatted by the engine.
type ForeignExplainProperty struct{ Name, Value string }

// ForeignExplainQueryPlan is the selected composite representation. Its Path.Plan
// is authoritative. The enclosing request's base Size/Path/Plan are unused when
// Query is nonnil; its ForeignRequest anchors the first genuine source.
type ForeignExplainQueryPlan struct {
	Request ForeignQueryRequest
	Path ForeignQueryPath
}

type ForeignExplainScanRequest struct {
	ForeignRequest
	Size    ForeignRelSize
	Path    ForeignPath
	Plan    ForeignPlan
	Query   *ForeignExplainQueryPlan
	Options ForeignExplainOptions
}

type ForeignExplainModifyRequest struct {
	ForeignModifyRequest
	Options ForeignExplainOptions
}

type ForeignExplainScanProvider interface {
	ExplainForeignScan(context.Context, ForeignExplainScanRequest) ([]ForeignExplainProperty, error)
}

type ForeignExplainModifyProvider interface {
	ExplainForeignModify(context.Context, ForeignExplainModifyRequest) ([]ForeignExplainProperty, error)
}

// ForeignSample contains actual sampled rows, not extrapolated or padded rows.
// Totals are separate finite nonnegative population estimates. Acquisition owns
// and closes all resources before returning, including on failure/cancellation.
type ForeignSample struct {
	Rows                                 []ForeignRow
	TotalRows, TotalDeadRows, TotalPages float64
}

type ForeignSampleRowsFunc func(context.Context, ForeignRequest, int) (ForeignSample, error)

type ForeignAnalyzeInfo struct {
	AcquireSampleRows ForeignSampleRowsFunc
}

// False means unsupported, distinct from an error obtaining provider facts.
// A supported result must supply a nonnil acquisition function.
// Negotiation receives owner identity and relation metadata but no mapping or
// transaction; it must not perform mapping-dependent I/O. Only successful
// acquisition receives the full owner's exact/PUBLIC-mapped request.
type ForeignAnalyzeProvider interface {
	AnalyzeForeignTable(context.Context, ForeignRequest) (ForeignAnalyzeInfo, bool, error)
}
