package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Query and catalog replacement share one physical-to-live head selection.
func selectQuerySource(ctx context.Context, source *querySource, frontier ids.CommitSeq, ends *storage.DeletionRoot, own *visibility.PrivateDeletes) (*querySource, error) {
	if source.rlsDenied {
		return source, ctx.Err()
	}
	replacements, single := 0, 0
	for i, row := range source.own {
		if row.Base != 0 {
			replacements++
			single = i
		}
	}
	if ends == nil && own == nil && replacements == 0 && !source.needsBirthFilter {
		return source, nil
	}
	total := len(source.committed) + len(source.own)
	var heads map[ids.VersionID]int
	singleSubstituted := false
	if replacements > 1 {
		heads = make(map[ids.VersionID]int, replacements)
		for i, row := range source.own {
			if row.Base != 0 {
				heads[row.Base] = len(source.committed) + i
			}
		}
	}
	include := func(raw, ordinal int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		live := visibility.LiveVersion(source.versionAt(raw), frontier, ends, own)
		if source.visible == nil && (!live || raw != ordinal) {
			source.visible = make([]int, ordinal, total)
			for j := range ordinal {
				source.visible[j] = j
			}
		}
		if live && source.visible != nil {
			source.visible = append(source.visible, raw)
		}
		return nil
	}
	for i, row := range source.committed {
		raw := i
		if replacements == 1 && row.Row == source.own[single].Row && row.Version == source.own[single].Base {
			raw = len(source.committed) + single
			singleSubstituted = true
		}
		if head, ok := heads[row.Version]; ok && source.versionAt(head).Row == row.Row {
			raw = head
			delete(heads, row.Version)
		}
		if err := include(raw, i); err != nil {
			source.close()
			return nil, err
		}
	}
	for i, row := range source.own {
		if row.Base != 0 && ((replacements == 1 && singleSubstituted) || (replacements > 1 && heads[row.Base] == 0)) {
			continue
		}
		raw := len(source.committed) + i
		if err := include(raw, raw); err != nil {
			source.close()
			return nil, err
		}
	}
	return source, nil
}

func partialMaskContains(root *storage.IndexRoot, row storage.RowVersion) bool {
	version, ok := root.Membership[row.Row]
	return ok && version == row.Version
}

type inheritanceQuerySegment struct {
	leaf    queryLeaf
	prefix  *querySource
	layout  []int // Nil is an identity layout: borrow the physical tuple.
	scratch []sqltype.Value
}

func (s *querySource) selectInheritancePrefixes(ctx context.Context) error {
	total := 0
	for i, segment := range s.inheritance {
		data := segment.prefix.view.Data()
		if _, err := selectQuerySource(ctx, segment.prefix, data.FrontierAt(segment.leaf.ref.Address), data.Ends[segment.leaf.ref.Address], data.Deleted[segment.leaf.ref.Address]); err != nil {
			return err
		}
		total += segment.prefix.count()
		s.inheritanceEnds[i] = total
	}
	return ctx.Err()
}

func (s *querySource) inheritanceSegment(index int) (*inheritanceQuerySegment, int) {
	at := sort.SearchInts(s.inheritanceEnds, index+1)
	if at > 0 {
		index -= s.inheritanceEnds[at-1]
	}
	return &s.inheritance[at], index
}

func (segment *inheritanceQuerySegment) mapValues(values []sqltype.Value) []sqltype.Value {
	if segment.layout == nil {
		return values
	}
	for i, slot := range segment.layout {
		segment.scratch[i] = values[slot]
	}
	return segment.scratch
}

// A locator preserves physical authority and raw storage hint independently of
// the named-parent layout and the currently cached candidate row version.
// Its segment is capture-owned, so refreshed values reuse mapping scratch.
// Provenance retains the original visible index through every existing adapter;
// caches retain physical versions, never a reconstructed parent storage.RowVersion.
type queryPhysicalRow struct {
	leaf    queryLeaf
	version storage.RowVersion
	hint    int
	segment *inheritanceQuerySegment
}

func (s *querySource) physicalRow(index int, logical queryLeaf, candidate storage.RowVersion) queryPhysicalRow {
	prefix := s
	var segment *inheritanceQuerySegment
	if s.inheritance != nil {
		segment, index = s.inheritanceSegment(index)
		prefix = segment.prefix
		logical = segment.leaf
	}
	raw := prefix.rawIndex(index)
	hint := raw
	if raw >= len(prefix.committed) {
		hint -= len(prefix.committed)
	}
	return queryPhysicalRow{leaf: logical, version: candidate, hint: hint, segment: segment}
}

func (row queryPhysicalRow) values(version storage.RowVersion) []sqltype.Value {
	if row.segment != nil {
		return row.segment.mapValues(version.Values)
	}
	return version.Values
}

func (s *querySource) physicalRef(index int) ids.ObjectRef {
	if s.inheritance != nil {
		segment, _ := s.inheritanceSegment(index)
		return segment.leaf.ref
	}
	return s.ref
}

// Definitions are snapshot-owned; completion uses the exact current incarnation.
type pgSequencesSource struct {
	host       execHost
	refs       []ids.ObjectRef
	predicates []expr.Expr
}

func openPGSequencesStage(state *queryState, source *querySource, schema []catalog.Column) indexedStage {
	p := source.pgSequences
	out := indexedStage{source: source, schema: schema, selection: []int{}}
	position := 0
	evaluation := state.evaluation(nil)
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		for position < len(p.refs) {
			i := position
			position++
			evaluation.setContext(ctx)
			evaluation.Row = source.anonymous[i]
			admitted := true
			for _, predicate := range p.predicates {
				value, err := predicate.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				truth, err := plan.TruthOf(value)
				if err != nil {
					return false, err
				}
				if truth != plan.True {
					admitted = false
					break
				}
			}
			if !admitted {
				continue
			}
			if err := p.host.CompletePGSequence(ctx, p.refs[i], source.anonymous[i]); err != nil {
				return false, err
			}
			out.selection = append(out.selection, i)
			return true, ctx.Err()
		}
		return false, ctx.Err()
	}
	return out
}

type capturedHashIndex struct {
	definition *catalog.IndexDefinition
	root       *storage.HashRoot
	usage      sqltype.Usage
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
func hashQueryFilter(root plan.Node) *plan.FilterNode {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return nil
	}
	child := project.Child
	for {
		switch node := child.(type) {
		case *plan.SortNode:
			child = node.Child
		case *plan.EvaluationNode:
			child = node.Child
		case *plan.FilterNode:
			if _, ok := node.Child.(*plan.SourceNode); ok {
				return node
			}
			return nil
		default:
			return nil
		}
	}
}

func hashFilterCandidates(ctx context.Context, state *queryState, n *plan.FilterNode, source *querySource) (*hashCandidateSelection, error) {
	if state.hashFilter != n {
		return nil, nil
	}
	if _, ok := n.Child.(*plan.SourceNode); !ok {
		return nil, nil
	}
	if source == nil || len(source.hashIndexes) == 0 || source.count() == 0 || source.input != nil || source.final != nil || source.materialized || source.nestedRoot != nil || len(source.own) != 0 || state.source.sources != nil {
		return nil, nil
	}
	predicate, ok := n.Predicate.(plan.ComparisonExpr)
	if !ok || predicate.Operator != "=" || predicate.Namespace != "" && predicate.Namespace != "pg_catalog" {
		return nil, nil
	}
	column, ok := predicate.Left.(plan.ColumnExpr)
	probe := predicate.Right
	if !ok {
		column, ok = predicate.Right.(plan.ColumnExpr)
		probe = predicate.Left
	}
	evaluation := state.evaluation(ctx)
	if !ok || column.Scope != evaluation.Scope || column.Frame != evaluation.Frame || column.Origin.Domain() != source.ref.Domain() || column.Origin.Epoch != source.ref.Epoch || column.Origin.Address.ClassID != source.ref.Address.ClassID || column.Origin.Address.ObjectID != source.ref.Address.ObjectID {
		return nil, nil
	}
	switch probe.(type) {
	case plan.LiteralExpr, plan.ParameterExpr:
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

// Each manifest entry is an SQL occurrence, even when physical identity repeats.
type queryLeaf struct {
	ref              ids.ObjectRef
	relation         *catalog.Relation
	storage          *storage.Relation
	only             bool
	principal        authorization.PrincipalRef // Relation authority, not expression/session identity.
	frozenAttributes []int32                    // Execution-owned stored-view subquery layout.
	uses             []int
	mark             plan.RowMark
	query            *parser.Select
	table            *parser.TableReference
	// DELETE's immediate USING occurrences require reference-kind validation,
	// not SELECT FOR UPDATE authority or source tuple locks.
	deleteReference  bool
	partialCandidate *catalog.Relation
	analysisOnly     bool // Unreferenced CTE body: analyzed dependency, no executable ACL.
}

func selectQuerySources(ctx context.Context, source *querySource) error {
	selectOne := func(source *querySource) error {
		if source.inheritance != nil {
			return source.selectInheritancePrefixes(ctx)
		}
		if source.view == nil {
			return ctx.Err()
		}
		data := source.view.Data()
		_, err := selectQuerySource(ctx, source, data.FrontierAt(source.ref.Address), data.Ends[source.ref.Address], data.Deleted[source.ref.Address])
		return err
	}
	if source.sources == nil {
		if err := selectOne(source); err != nil {
			source.close()
			return err
		}
		return nil
	}
	for _, leaf := range source.sources {
		if err := selectOne(leaf); err != nil {
			source.close()
			return err
		}
	}
	return nil
}
