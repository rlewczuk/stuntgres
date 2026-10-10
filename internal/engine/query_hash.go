package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

type capturedHashIndex struct {
	definition *indexDefinition
	root       *hashIndexRoot
	usage      sqltype.Usage
}

// The caller holds e.mu, the same owner as visibility-prefix capture. After
// this proof, probing consults only the retained root and retained source rows.
func (e *Engine) captureHashIndexesLocked(scope *StatementScope, leaf queryLeaf, source *querySource, mark queryRowMark) error {
	if !scope.implicit || scope.catalog != nil || mark.mode != 0 || source.view == nil || len(source.own) != 0 {
		return nil
	}
	data := source.view.read()
	if data.catalog.state != e.state {
		return nil
	}
	a := leaf.ref.Address
	store := data.generationAt(a)
	if store == nil || store != e.storage[a] || data.frontierAt(a) != e.commitSeq || data.deleted[a] != nil || len(source.committed) != len(store.versions) || data.ends[a] != store.ends {
		return nil
	}
	for address, carrier := range e.indexRoots {
		index, ok := data.catalog.state.descriptors[address].(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.method != hashMethod || index.sqlIndex.parent != a || carrier == nil || carrier.hash == nil {
			continue
		}
		// Readiness permits maintenance, not complete query coverage. A
		// grandfathered commit may legitimately be absent from an invalid root.
		if !index.sqlIndex.ready || !index.sqlIndex.valid || !index.sqlIndex.live {
			continue
		}
		live, ok := e.state.descriptors[address].(*relationDescriptor)
		if !ok || !sameIndexKeySemantics(index, live) {
			continue
		}
		root := carrier.hash
		if root.storage != store || root.generation != store.generation || root.prefix != len(source.committed) || root.ends != data.ends[a] {
			continue
		}
		usage, err := indexKeyUsage(leaf.relation, index.sqlIndex.keys[0])
		if err != nil {
			return err
		}
		if usage != root.binding.usage {
			continue
		}
		source.hashIndexes = append(source.hashIndexes, capturedHashIndex{definition: index.sqlIndex, root: root, usage: usage})
	}
	return nil
}

// This state is consumed by the real filter producer, not exported telemetry.
// Occurrences are local to the captured source's selected visibility mapping.
type hashCandidateSelection struct {
	root        *hashIndexRoot
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
	child := project.child
	for {
		switch node := child.(type) {
		case *sortNode:
			child = node.child
		case *evaluationNode:
			child = node.child
		case *filterNode:
			if _, ok := node.child.(*sourceNode); ok {
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
	if _, ok := n.child.(*sourceNode); !ok {
		return nil, nil
	}
	if source == nil || len(source.hashIndexes) == 0 || source.count() == 0 || source.input != nil || source.final != nil || source.materialized || source.nestedRoot != nil || len(source.own) != 0 || state.source.sources != nil {
		return nil, nil
	}
	predicate, ok := n.predicate.(comparisonExpression)
	if !ok || predicate.operator != "=" || predicate.namespace != "" && predicate.namespace != "pg_catalog" {
		return nil, nil
	}
	column, ok := predicate.left.(columnExpression)
	probe := predicate.right
	if !ok {
		column, ok = predicate.right.(columnExpression)
		probe = predicate.left
	}
	evaluation := state.evaluation(ctx)
	if !ok || column.scope != evaluation.Scope || column.frame != evaluation.Frame || column.origin.owner != source.ref.owner || column.origin.Epoch != source.ref.Epoch || column.origin.Address.ClassID != source.ref.Address.ClassID || column.origin.Address.ObjectID != source.ref.Address.ObjectID {
		return nil, nil
	}
	switch probe.(type) {
	case literalExpression, parameterExpression:
	default:
		return nil, nil
	}
	if probe.ResultUsage() != column.ResultUsage() || !predicate.predicate.EqualityFor(column.ResultUsage().Type) {
		return nil, nil
	}
	for _, capture := range source.hashIndexes {
		key := capture.definition.keys[0]
		if key.derived != nil || key.attribute != column.origin.Address.SubID || column.slot != capture.root.binding.slot || capture.usage != column.ResultUsage() || !capture.root.binding.provider.OrdinaryEquality() {
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
		h, err := capture.root.binding.provider.Hash(ctx, value)
		if err != nil {
			return nil, err
		}
		bucket := capture.root.buckets[h]
		if len(bucket) != 0 {
			selection.occurrences = make([]int, 0, len(bucket))
		}
		for _, entry := range bucket {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.position < 0 || entry.position >= len(source.committed) {
				return nil, fmt.Errorf("stuntgres: invalid hash candidate position")
			}
			row := source.committed[entry.position]
			if row.row != entry.row || row.version != entry.version {
				return nil, fmt.Errorf("stuntgres: invalid hash candidate identity")
			}
			ordinal := entry.position
			if source.visible != nil {
				var found bool
				ordinal, found = slices.BinarySearch(source.visible, entry.position)
				if !found {
					return nil, fmt.Errorf("stuntgres: incomplete hash candidate visibility")
				}
			}
			equal, err := capture.root.binding.provider.Equal(ctx, entry.key, value)
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
