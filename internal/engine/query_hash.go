package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type capturedHashIndex struct {
	definition *catalog.IndexDefinition
	root       *storage.HashRoot
	usage      sqltype.Usage
}

// The caller holds e.mu, the same owner as visibility-prefix capture. After
// this proof, probing consults only the retained root and retained source rows.
func (e *Engine) captureHashIndexesLocked(scope *StatementScope, leaf queryLeaf, source *querySource, mark queryRowMark) error {
	if !scope.implicit || scope.catalog != nil || mark.Mode != 0 || source.view == nil || source.inheritance != nil || len(source.own) != 0 {
		return nil
	}
	data := source.view.Data()
	if data.Catalog.State() != e.catalog.state {
		return nil
	}
	a := leaf.ref.Address
	store := data.GenerationAt(a)
	if store == nil || store != e.storage.Relations[a] || data.FrontierAt(a) != e.visibility.CommitSeq() || data.Deleted[a] != nil || len(source.committed) != len(store.Versions) || data.Ends[a] != store.Ends {
		return nil
	}
	for address, carrier := range e.storage.IndexRoots {
		index, ok := data.Catalog.Relation(address)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Method != catalog.HashMethod || index.SQLIndex.Parent != a || carrier == nil || carrier.Hash == nil {
			continue
		}
		// Readiness permits maintenance, not complete query coverage. A
		// grandfathered commit may legitimately be absent from an invalid root.
		if !index.SQLIndex.Ready || !index.SQLIndex.Valid || !index.SQLIndex.Live {
			continue
		}
		live, ok := e.catalog.state.Relation(address)
		if !ok || !sameIndexKeySemantics(index, live) {
			continue
		}
		root := carrier.Hash
		if root.Storage != store || root.Generation != store.Generation || root.Prefix != len(source.committed) || root.Ends != data.Ends[a] {
			continue
		}
		usage, err := indexKeyUsage(leaf.relation, index.SQLIndex.Keys[0])
		if err != nil {
			return err
		}
		if usage != root.Binding.Usage {
			continue
		}
		source.hashIndexes = append(source.hashIndexes, capturedHashIndex{definition: index.SQLIndex, root: root, usage: usage})
	}
	return nil
}

// This state is consumed by the real filter producer, not exported telemetry.
// Occurrences are local to the captured source's selected visibility mapping.
type hashCandidateSelection struct {
	root        *storage.HashRoot
	occurrences []int
	position    int
}

// Select only a top-level, single-source WHERE. Inner view/derived/subquery
// execution constructs its own state and never inherits this eligibility.
func hashQueryFilter(root LogicalNode) *filterNode {
	project, ok := root.(*projectNode)
	if !ok {
		return nil
	}
	child := project.Child
	for {
		switch node := child.(type) {
		case *sortNode:
			child = node.Child
		case *evaluationNode:
			child = node.Child
		case *filterNode:
			if _, ok := node.Child.(*sourceNode); ok {
				return node
			}
			return nil
		default:
			return nil
		}
	}
}

func hashFilterCandidates(ctx context.Context, state *queryState, n *filterNode, source *querySource) (*hashCandidateSelection, error) {
	if state.hashFilter != n {
		return nil, nil
	}
	if _, ok := n.Child.(*sourceNode); !ok {
		return nil, nil
	}
	if source == nil || len(source.hashIndexes) == 0 || source.count() == 0 || source.input != nil || source.final != nil || source.materialized || source.nestedRoot != nil || len(source.own) != 0 || state.source.sources != nil {
		return nil, nil
	}
	predicate, ok := n.Predicate.(comparisonExpression)
	if !ok || predicate.Operator != "=" || predicate.Namespace != "" && predicate.Namespace != "pg_catalog" {
		return nil, nil
	}
	column, ok := predicate.Left.(columnExpression)
	probe := predicate.Right
	if !ok {
		column, ok = predicate.Right.(columnExpression)
		probe = predicate.Left
	}
	evaluation := state.evaluation(ctx)
	if !ok || column.Scope != evaluation.Scope || column.Frame != evaluation.Frame || column.Origin.Domain() != source.ref.Domain() || column.Origin.Epoch != source.ref.Epoch || column.Origin.Address.ClassID != source.ref.Address.ClassID || column.Origin.Address.ObjectID != source.ref.Address.ObjectID {
		return nil, nil
	}
	switch probe.(type) {
	case literalExpression, parameterExpression:
	default:
		return nil, nil
	}
	if probe.ResultUsage() != column.ResultUsage() || !predicate.Predicate.EqualityFor(column.ResultUsage()) {
		return nil, nil
	}
	for _, capture := range source.hashIndexes {
		key := capture.definition.Keys[0]
		if key.Derived != nil || key.Attribute != column.Origin.Address.SubID || column.Slot != capture.root.Binding.Slot || capture.usage != column.ResultUsage() || !capture.root.Binding.Provider.OrdinaryEquality() {
			continue
		}
		value, err := probe.Eval(&evaluation)
		if err != nil {
			return nil, err
		}
		selection := &hashCandidateSelection{root: capture.root, occurrences: []int{}}
		if value.IsNull() {
			return selection, ctx.Err()
		}
		h, err := capture.root.Binding.Provider.Hash(ctx, value)
		if err != nil {
			return nil, err
		}
		bucket := capture.root.Buckets[h]
		if len(bucket) != 0 {
			selection.occurrences = make([]int, 0, len(bucket))
		}
		for _, entry := range bucket {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.Position < 0 || entry.Position >= len(source.committed) {
				return nil, fmt.Errorf("stuntgres: invalid hash candidate position")
			}
			row := source.committed[entry.Position]
			if row.Row != entry.Row || row.Version != entry.Version {
				return nil, fmt.Errorf("stuntgres: invalid hash candidate identity")
			}
			ordinal := entry.Position
			if source.visible != nil {
				var found bool
				ordinal, found = slices.BinarySearch(source.visible, entry.Position)
				if !found {
					return nil, fmt.Errorf("stuntgres: incomplete hash candidate visibility")
				}
			}
			equal, err := capture.root.Binding.Provider.Equal(ctx, entry.Key, value)
			if err != nil {
				return nil, err
			}
			if equal {
				selection.occurrences = append(selection.occurrences, ordinal)
			}
		}
		return selection, ctx.Err()
	}
	return nil, nil
}
