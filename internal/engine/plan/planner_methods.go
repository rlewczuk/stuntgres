package plan

// PlannerMethods is an admitted value, never a live settings owner. Disabled
// bits make detached/internal zero values preserve existing enabled algorithms.
// Async and parallel authority remain separate admission capabilities.
type PlannerMethods struct {
	NoHashJoin bool
	NoHashAgg bool
	NoIndexScan bool
	NoSeqScan bool
	NoPartitionPruning bool
	NoGatherMerge bool
}
