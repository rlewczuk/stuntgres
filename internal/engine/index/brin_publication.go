package index

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// MergeBrinIndexRoot copies each touched summary once. Ended rows leave safe
// conservative bounds; only a genuine rebuild may shrink or renumber ranges.
func MergeBrinIndexRoot(evaluation *exec.Context, parent, index *catalog.Relation, old *storage.IndexRoot, p *RowPublication, reconstruct, materialize bool) (*storage.IndexRoot, error) {
	bindings, err := BindBrinIndex(evaluation.Catalog, parent, index.SQLIndex)
	if err != nil {
		return nil, err
	}
	if materialize && (old == nil || old.Brin == nil) {
		return nil, fmt.Errorf("stuntgres: missing BRIN root")
	}
	o := brinKeyProgram(bindings, index.SQLIndex)
	var preparation ExecutionPreparation
	var root *storage.BrinRoot
	var copied map[int]bool
	var writes map[int]storage.RowVersion
	_, auto := BrinOptions(index)
	if materialize {
		next := *old.Brin
		next.Bindings = bindings
		next.Storage, next.Generation, next.Prefix = p.Store, p.Generation+1, len(p.Next)
		next.Ends = p.Ends
		if p.NextEnds != nil {
			next.Ends = p.NextEnds
		}
		root = &next
		count := (len(p.Next) + root.EffectivePagesPerRange - 1) / root.EffectivePagesPerRange
		if len(p.Writes) != 0 {
			root.Ranges = make([]storage.BrinRange, max(count, len(old.Brin.Ranges)))
			copy(root.Ranges, old.Brin.Ranges)
			copied = make(map[int]bool)
		}
		if auto && count > len(old.Brin.Ranges) {
			writes = make(map[int]storage.RowVersion, len(p.Writes))
			for i, row := range p.Writes {
				writes[PublicationSlot(p, i)] = row
			}
		}
	}
	add := func(summary *storage.BrinRange, row storage.RowVersion, checkEnrollment bool) error {
		if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), p.Ends, p.Deletion) {
			return nil
		}
		output, produced := row.IndexOutputs[index.Address()]
		produced = produced && OutputMatches(output, index)
		if checkEnrollment && !reconstruct && !produced && !RowMaintainsIndex(row, index) {
			return nil
		}
		if produced && !output.Member {
			return nil
		}
		if !produced {
			matches, err := preparation.PredicateMatches(evaluation, parent, index.SQLIndex, row.Values, &o, nil)
			if err != nil || !matches {
				return err
			}
			if err := preparation.PrepareKeys(evaluation, parent, index.SQLIndex, &o, nil); err != nil {
				return err
			}
		}
		for i, b := range bindings {
			var program expr.Expr
			if o.Derived != nil {
				program = o.Derived[i]
			}
			var err error
			value := output.Key
			if produced {
				if len(output.Keys) != len(bindings) {
					return fmt.Errorf("stuntgres: incomplete BRIN insertion output")
				}
				value = output.Keys[i]
			} else {
				value, err = extractIndexKey(b.Slot, program, row.Values, evaluation)
			}
			if err != nil {
				return err
			}
			if summary != nil {
				if err := extendBrinSummary(evaluation, &summary.Keys[i], b, value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i, row := range p.Writes {
		if err := evaluation.Context.Err(); err != nil {
			return nil, err
		}
		if !materialize {
			if err := add(nil, row, true); err != nil {
				return nil, err
			}
			continue
		}
		ordinal := PublicationSlot(p, i) / root.EffectivePagesPerRange
		if root.Ranges[ordinal].Summarized {
			if !copied[ordinal] {
				root.Ranges[ordinal].Keys = slices.Clone(root.Ranges[ordinal].Keys)
				copied[ordinal] = true
			}
			if err := add(&root.Ranges[ordinal], row, true); err != nil {
				return nil, err
			}
		}
	}
	if writes != nil {
		width := root.EffectivePagesPerRange
		for ordinal := len(old.Brin.Ranges); ordinal < len(root.Ranges); ordinal++ {
			if ordinal == 0 || root.Ranges[ordinal-1].Summarized {
				continue
			}
			previous := storage.BrinRange{Summarized: true, Keys: make([]storage.BrinSummary, len(bindings))}
			occupied := false
			for slot := (ordinal - 1) * width; slot < ordinal*width; slot++ {
				if err := evaluation.Context.Err(); err != nil {
					return nil, err
				}
				row, ok := writes[slot]
				if !ok && slot < len(p.Root) {
					row, ok = p.Root[slot], true
				}
				if !ok {
					continue
				}
				occupied = true
				_, written := writes[slot]
				if err := add(&previous, row, written); err != nil {
					return nil, err
				}
			}
			if occupied {
				root.Ranges[ordinal-1] = previous
			}
		}
	}
	if !materialize {
		return nil, evaluation.Context.Err()
	}
	return &storage.IndexRoot{Brin: root}, evaluation.Context.Err()
}
