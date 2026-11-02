package exec

import (
	"context"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Mixed hierarchies retain their admitted physical captures. Heap prefixes keep
// their visibility pins; foreign prefixes open the real handler on demand.
func openForeignHierarchy(ctx context.Context, state *State, source *Source, node *plan.SourceNode) (Stage, error) {
	source.Materialized, source.FlatWidth = true, len(node.Schema())
	resetOutput := func(out *Stage) {
		source.Flat, source.FlatCount = source.Flat[:0], 0
		source.PhysicalRefs = source.PhysicalRefs[:0]
		source.Provenance, source.Versions = source.Provenance[:0], source.Versions[:0]
		source.VersionValid = nil
		out.Selection, out.Done = out.Selection[:0], false
	}
	out := Stage{Source: source, Columns: node.Schema(), Selection: []int{}}
	resetOutput(&out)
	var child Stage
	var row []sqltype.Value
	position, opened := 0, false
	closeChild := func() error {
		if !opened {
			return nil
		}
		opened = false
		return child.Close()
	}
	out.Finish = closeChild
	out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
		if err := closeChild(); err != nil {
			return false, err
		}
		position = 0
		resetOutput(out)
		return true, ctx.Err()
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		for position < len(source.Inheritance) {
			segment := &source.Inheritance[position]
			if !opened {
				if segment.Prefix.Foreign != nil {
					var err error
					childNode := &plan.SourceNode{QuerySchema: segment.Leaf.Relation.Columns, Ref: segment.Leaf.Ref, RangeName: segment.Leaf.Relation.Name()}
					child, err = openForeignStage(ctx, state, segment.Prefix, childNode, segment.Leaf.Relation.Columns)
					if err != nil {
						return false, err
					}
				} else {
					child = Stage{Source: segment.Prefix, Columns: segment.Leaf.Relation.Columns}
				}
				width := len(segment.Leaf.Relation.Columns)
				if cap(row) < width {
					row = make([]sqltype.Value, width)
				} else {
					row = row[:width]
				}
				opened = true
			}
			ordinal, present, err := child.NextOccurrence(ctx)
			if err != nil {
				return false, err
			}
			if !present {
				if err := closeChild(); err != nil {
					return false, err
				}
				position++
				continue
			}
			if _, err := child.ProjectOrdinal(ctx, ordinal, row); err != nil {
				return false, err
			}
			index := child.Index(ordinal)
			source.Flat = append(source.Flat, segment.MapValues(row)...)
			source.PhysicalRefs = append(source.PhysicalRefs, segment.Leaf.Ref)
			for i := range source.Occurrences {
				visible := AbsentQueryIndex
				if i == position {
					visible = index
				}
				source.Provenance = append(source.Provenance, visible)
				if source.Locks.RowMarked() {
					if i == position {
						source.Versions = AppendQueryVersions(source.Versions, segment.Prefix, index, source.Locks)
					} else {
						source.Versions = append(source.Versions, storage.RowVersion{})
					}
				}
			}
			out.Selection = append(out.Selection, source.FlatCount)
			source.FlatCount++
			return true, ctx.Err()
		}
		return false, ctx.Err()
	}
	if source.RLS != nil {
		return openRowSecurityStage(ctx, state, out), nil
	}
	return out, nil
}
