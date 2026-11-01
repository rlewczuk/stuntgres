package exec

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

type CapturedBloomIndex struct {
	Address    ids.ObjectAddress
	Name       string
	Definition *catalog.IndexDefinition
	Root       *storage.IndexRoot
}

func bloomProbeKey(capture CapturedBloomIndex, probe plan.BloomProbe) int {
	for i, key := range capture.Definition.Keys {
		binding := capture.Root.Bloom.Bindings[i]
		if key.Derived == nil && key.Attribute == probe.Column.Origin.Address.SubID && binding.Slot == probe.Column.Slot && binding.Usage == probe.Column.ResultUsage() && binding.Provider.OrdinaryEquality() {
			return i
		}
	}
	return -1
}

// ChooseBloomScan adds a distinct method without changing hash's established
// policy. Inputs are detached metadata; no probe evaluation occurs here.
func ChooseBloomScan(current plan.ScanDecision, filter *plan.FilterNode, ref ids.ObjectRef, scope, frame uint32, methods plan.PlannerMethods, costs plan.PlannerCosts, rows, fraction float64, indexes []CapturedBloomIndex) plan.ScanDecision {
	if methods.NoIndexScan || filter == nil || len(indexes) == 0 {
		return current
	}
	probes := plan.BloomFilterProbes(filter, ref, scope, frame)
	for _, capture := range indexes {
		count := 0
		for _, probe := range probes {
			if bloomProbeKey(capture, probe) >= 0 {
				count++
			}
		}
		if count == 0 {
			continue
		}
		candidate := costs.BloomScan(rows, fraction, capture.Address, capture.Name, capture.Root.Bloom.Options.Words, count)
		if methods.NoSeqScan {
			candidate.Method = plan.BloomCandidates
		}
		if candidate.Method != plan.BloomCandidates {
			continue
		}
		if current.Method == plan.FullScan || candidate.CandidateScore() < current.CandidateScore() {
			current = candidate
		}
	}
	return current
}

func bloomFilterCandidates(ctx context.Context, state *State, n *plan.FilterNode, source *Source) (*candidateSelection, error) {
	state.CapturePlannerAdmission()
	if state.Methods.NoIndexScan || state.HashFilter != n || source == nil || source.ScanDecision.Method != plan.BloomCandidates || source.RLS != nil || source.RlsDenied || source.Input != nil || source.Final != nil || source.Materialized || source.NestedRoot != nil || len(source.Own) != 0 || state.Source.Sources != nil {
		return nil, nil
	}
	if _, ok := n.Child.(*plan.SourceNode); !ok {
		return nil, nil
	}
	evaluation := state.Evaluation(ctx)
	probes := plan.BloomFilterProbes(n, source.Ref, evaluation.Scope, evaluation.Frame)
	for _, capture := range source.BloomIndexes {
		if capture.Address != source.ScanDecision.Index {
			continue
		}
		root := capture.Root.Bloom
		var scratch [256]uint16
		mask := scratch[:root.Options.Words]
		selection := &candidateSelection{Bloom: root, Occurrences: []int{}}
		constrained := false
		for _, probe := range probes {
			key := bloomProbeKey(capture, probe)
			if key < 0 {
				continue
			}
			constrained = true
			value, err := probe.Value.Eval(&evaluation)
			if err != nil {
				return nil, err
			}
			if value.IsNull() {
				return selection, ctx.Err()
			}
			if err := storage.AddBloomValue(ctx, mask, root.Options, key, &root.Bindings[key].Provider, value); err != nil {
				return nil, err
			}
		}
		if !constrained {
			return nil, nil
		}
		for i, signed := range root.Entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !storage.BloomContains(signed.Signature, mask) {
				continue
			}
			entry := capture.Root.Entries[i]
			if entry.Position < 0 || entry.Position >= len(source.Committed) {
				return nil, fmt.Errorf("stuntgres: invalid bloom candidate position")
			}
			row := source.Committed[entry.Position]
			if row.Row != entry.Row || row.Version != entry.Version {
				return nil, fmt.Errorf("stuntgres: invalid bloom candidate identity")
			}
			ordinal := entry.Position
			if source.Visible != nil {
				var found bool
				ordinal, found = slices.BinarySearch(source.Visible, entry.Position)
				if !found {
					return nil, fmt.Errorf("stuntgres: incomplete bloom candidate visibility")
				}
			}
			// Deliberately no key comparison: collisions reach the original
			// FilterNode predicate on the captured visible stored row.
			selection.Occurrences = append(selection.Occurrences, ordinal)
		}
		return selection, ctx.Err()
	}
	return nil, nil
}
