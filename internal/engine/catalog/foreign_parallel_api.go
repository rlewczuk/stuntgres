package catalog

import "context"

// ForeignParallelScanProvider optionally supplies disjoint, exhaustive occurrence
// partitions. Safety must be resource-free; false retains the ordinary scan.
// Plans are immutable and reusable; mutable execution state belongs to Begin's
// result. The request owns its metadata and parameter cells.
type ForeignParallelScanProvider interface {
	IsForeignScanParallelSafe(context.Context, ForeignRequest, ForeignPlan) (bool, error)
	BeginForeignParallelScan(context.Context, ForeignRequest, ForeignPlan, int) (ForeignParallelScan, error)
}

// ForeignParallelScan owns one execution's common source frontier. The maximum
// passed to Begin is a ceiling, not a row estimate. PartitionCount is immutable
// and in [1, maximum]. The leader opens each distinct partition exactly once,
// serially; only distinct scans' Iterate callbacks run concurrently. Each scan
// retains the ordinary borrowed-cell and cleanup contract. A nonnil Begin result
// transfers cleanup ownership even with an error. End runs after all scans join
// and end, without cancellation, once. No PostgreSQL process or DSM ABI is implied.
type ForeignParallelScan interface {
	PartitionCount() int
	BeginPartitionForeignScan(context.Context, int) (ForeignScan, error)
	EndForeignParallelScan(context.Context) error
}
