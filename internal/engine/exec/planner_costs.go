package exec

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// ChooseScan is metadata-only: it neither evaluates nor hashes the probe. The
// final source visibility/root checks remain at hashFilterCandidates.
func ChooseScan(filter *plan.FilterNode, ref ids.ObjectRef, scope, frame uint32, methods plan.PlannerMethods, costs plan.PlannerCosts, rows, fraction float64, indexes []CapturedHashIndex) plan.ScanDecision {
	full := costs.Scan(rows, fraction, ids.ObjectAddress{}, "")
	if methods.NoIndexScan || filter == nil {
		return full
	}
	column, _, ok := plan.HashFilterProbe(filter, ref, scope, frame)
	if !ok {
		return full
	}
	for _, capture := range indexes {
		key := capture.Definition.Keys[0]
		if key.Derived == nil && key.Attribute == column.Origin.Address.SubID && column.Slot == capture.Root.Binding.Slot && capture.Usage == column.ResultUsage() && capture.Root.Binding.Provider.OrdinaryEquality() {
			decision := costs.Scan(rows, fraction, capture.Address, capture.Name)
			if methods.NoSeqScan {
				decision.Method = plan.HashCandidates
			}
			return decision
		}
	}
	return full
}

// AdmitScan copies a detached decision onto this execution-owned source before
// a cursor/portal can retain it. Reopening that source never reads settings.
func AdmitScan(root plan.Node, source *Source) {
	if source == nil {
		return
	}
	filter := HashQueryFilter(root)
	frame := uint32(1)
	if project, ok := root.(*plan.ProjectNode); ok && project.Frame != 0 {
		frame = project.Frame
	}
	indexes := source.HashIndexes
	if source.RLS != nil || source.RlsDenied || source.Input != nil || source.Final != nil || source.Materialized || source.NestedRoot != nil || len(source.Own) != 0 || source.Sources != nil {
		indexes = nil
	}
	fraction := source.ScanFraction
	if fraction == 0 {
		fraction = 1
	} // Sources constructed without cursor admission are ordinary.
	source.ScanDecision = ChooseScan(filter, source.Ref, 1, frame, sourcePlannerMethods(source), source.PlannerCosts, source.PlannerRows, fraction, indexes)
}
