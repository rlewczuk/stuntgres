package session

import "stuntgres/internal/engine/plan"

// PlannerMethodsLocked captures effective values while the caller holds Mu.
func (s *SettingsStore) PlannerMethodsLocked() plan.PlannerMethods {
	return plan.PlannerMethods{
		NoHashJoin: !s.TypedValue("enable_hashjoin").Boolean,
		NoHashAgg: !s.TypedValue("enable_hashagg").Boolean,
		NoIndexScan: !s.TypedValue("enable_indexscan").Boolean,
		NoSeqScan: !s.TypedValue("enable_seqscan").Boolean,
		NoPartitionPruning: !s.TypedValue("enable_partition_pruning").Boolean,
		NoGatherMerge: !s.TypedValue("enable_gathermerge").Boolean,
	}
}
