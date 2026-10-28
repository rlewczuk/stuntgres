package exec

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
func SelectQuerySource(ctx context.Context, source *Source, frontier ids.CommitSeq, ends *storage.DeletionRoot, own *visibility.PrivateDeletes, heap visibility.HeapOperations) (*Source, error) {
	if source.RlsDenied {
		return source, ctx.Err()
	}
	replacements, single := 0, 0
	for i, row := range source.Own {
		if row.Base != 0 {
			replacements++
			single = i
		}
	}
	if ends == nil && own == nil && replacements == 0 && !source.NeedsBirthFilter {
		return source, nil
	}
	total := len(source.Committed) + len(source.Own)
	var heads map[ids.VersionID]int
	singleSubstituted := false
	if replacements > 1 {
		heads = make(map[ids.VersionID]int, replacements)
		for i, row := range source.Own {
			if row.Base != 0 {
				heads[row.Base] = len(source.Committed) + i
			}
		}
	}
	include := func(raw, ordinal int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		live := heap.LiveVersion(source.VersionAt(raw), frontier, ends, own)
		if source.Visible == nil && (!live || raw != ordinal) {
			source.Visible = make([]int, ordinal, total)
			for j := range ordinal {
				source.Visible[j] = j
			}
		}
		if live && source.Visible != nil {
			source.Visible = append(source.Visible, raw)
		}
		return nil
	}
	for i, row := range source.Committed {
		raw := i
		if replacements == 1 && row.Row == source.Own[single].Row && row.Version == source.Own[single].Base {
			raw = len(source.Committed) + single
			singleSubstituted = true
		}
		if head, ok := heads[row.Version]; ok && source.VersionAt(head).Row == row.Row {
			raw = head
			delete(heads, row.Version)
		}
		if err := include(raw, i); err != nil {
			source.Close()
			return nil, err
		}
	}
	for i, row := range source.Own {
		if row.Base != 0 && ((replacements == 1 && singleSubstituted) || (replacements > 1 && heads[row.Base] == 0)) {
			continue
		}
		raw := len(source.Committed) + i
		if err := include(raw, raw); err != nil {
			source.Close()
			return nil, err
		}
	}
	return source, nil
}

func PartialMaskContains(root *storage.IndexRoot, row storage.RowVersion) bool {
	version, ok := root.Membership[row.Row]
	return ok && version == row.Version
}

type InheritanceQuerySegment struct {
	Leaf    Leaf
	Prefix  *Source
	Layout  []int // Nil is an identity layout: borrow the physical tuple.
	Scratch []sqltype.Value
}

func (s *Source) selectInheritancePrefixes(ctx context.Context) error {
	total := 0
	for i, segment := range s.Inheritance {
		data := segment.Prefix.View.Data()
		heap, err := visibility.ResolveRelationHeapAccess(data.Catalog, segment.Prefix.Ref)
		if err != nil {
			return err
		}
		if _, err := SelectQuerySource(ctx, segment.Prefix, data.FrontierAt(segment.Leaf.Ref.Address), data.Ends[segment.Leaf.Ref.Address], data.Deleted[segment.Leaf.Ref.Address], heap); err != nil {
			return err
		}
		total += segment.Prefix.Count()
		s.InheritanceEnds[i] = total
	}
	return ctx.Err()
}

func (s *Source) inheritanceSegment(index int) (*InheritanceQuerySegment, int) {
	at := sort.SearchInts(s.InheritanceEnds, index+1)
	if at > 0 {
		index -= s.InheritanceEnds[at-1]
	}
	return &s.Inheritance[at], index
}

func (segment *InheritanceQuerySegment) MapValues(values []sqltype.Value) []sqltype.Value {
	if segment.Layout == nil {
		return values
	}
	for i, slot := range segment.Layout {
		segment.Scratch[i] = values[slot]
	}
	return segment.Scratch
}

// A locator preserves physical authority and raw storage hint independently of
// the named-parent layout and the currently cached candidate row version.
// Its segment is capture-owned, so refreshed values reuse mapping scratch.
// Provenance retains the original visible index through every existing adapter;
// caches retain physical versions, never a reconstructed parent storage.RowVersion.
type QueryPhysicalRow struct {
	Leaf    Leaf
	Version storage.RowVersion
	Hint    int
	Segment *InheritanceQuerySegment
}

func (s *Source) PhysicalRow(index int, logical Leaf, candidate storage.RowVersion) QueryPhysicalRow {
	prefix := s
	var segment *InheritanceQuerySegment
	if s.Inheritance != nil {
		segment, index = s.inheritanceSegment(index)
		prefix = segment.Prefix
		logical = segment.Leaf
	}
	raw := prefix.RawIndex(index)
	hint := raw
	if raw >= len(prefix.Committed) {
		hint -= len(prefix.Committed)
	}
	return QueryPhysicalRow{Leaf: logical, Version: candidate, Hint: hint, Segment: segment}
}

func (row QueryPhysicalRow) Values(version storage.RowVersion) []sqltype.Value {
	if row.Segment != nil {
		return row.Segment.MapValues(version.Values)
	}
	return version.Values
}

func (s *Source) PhysicalRef(index int) ids.ObjectRef {
	if s.Inheritance != nil {
		segment, _ := s.inheritanceSegment(index)
		return segment.Leaf.Ref
	}
	return s.Ref
}

// Definitions are snapshot-owned; completion uses the exact current incarnation.
type PGSequencesSource struct {
	Host       Host
	Refs       []ids.ObjectRef
	Predicates []expr.Expr
}

func openPGSequencesStage(state *State, source *Source, schema []catalog.Column) Stage {
	p := source.PGSequences
	out := Stage{Source: source, Columns: schema, Selection: []int{}}
	position := 0
	evaluation := state.Evaluation(nil)
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		for position < len(p.Refs) {
			i := position
			position++
			evaluation.setContext(ctx)
			evaluation.Row = source.Anonymous[i]
			admitted := true
			for _, predicate := range p.Predicates {
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
			if err := p.Host.CompletePGSequence(ctx, p.Refs[i], source.Anonymous[i]); err != nil {
				return false, err
			}
			out.Selection = append(out.Selection, i)
			return true, ctx.Err()
		}
		return false, ctx.Err()
	}
	return out
}

type CapturedHashIndex struct {
	Address    ids.ObjectAddress
	Name       string
	Definition *catalog.IndexDefinition
	Root       *storage.HashRoot
	Usage      sqltype.Usage
}

// This state is consumed by the real filter producer, not exported telemetry.
// Occurrences are local to the captured source's selected visibility mapping.
type hashCandidateSelection struct {
	Root        *storage.HashRoot
	Occurrences []int
	Position    int
}

// Select only a top-level, single-source WHERE. Inner view/derived/subquery
// execution constructs its own state and never inherits this eligibility.
func HashQueryFilter(root plan.Node) *plan.FilterNode {
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

func hashFilterCandidates(ctx context.Context, state *State, n *plan.FilterNode, source *Source) (*hashCandidateSelection, error) {
	state.CapturePlannerAdmission()
	if state.Methods.NoIndexScan || state.HashFilter != n || source == nil || source.ScanDecision.Method != plan.HashCandidates {
		return nil, nil
	}
	if _, ok := n.Child.(*plan.SourceNode); !ok {
		return nil, nil
	}
	if source == nil || source.RLS != nil || len(source.HashIndexes) == 0 || source.Count() == 0 || source.Input != nil || source.Final != nil || source.Materialized || source.NestedRoot != nil || len(source.Own) != 0 || state.Source.Sources != nil {
		return nil, nil
	}
	evaluation := state.Evaluation(ctx)
	column, probe, ok := plan.HashFilterProbe(n, source.Ref, evaluation.Scope, evaluation.Frame)
	if !ok {
		return nil, nil
	}
	for _, capture := range source.HashIndexes {
		key := capture.Definition.Keys[0]
		if key.Derived != nil || key.Attribute != column.Origin.Address.SubID || column.Slot != capture.Root.Binding.Slot || capture.Usage != column.ResultUsage() || !capture.Root.Binding.Provider.OrdinaryEquality() {
			continue
		}
		if source.ScanDecision.Method != plan.HashCandidates || source.ScanDecision.Index != capture.Address {
			continue
		}
		value, err := probe.Eval(&evaluation)
		if err != nil {
			return nil, err
		}
		selection := &hashCandidateSelection{Root: capture.Root, Occurrences: []int{}}
		if value.IsNull() {
			return selection, ctx.Err()
		}
		h, err := capture.Root.Binding.Provider.Hash(ctx, value)
		if err != nil {
			return nil, err
		}
		bucket := capture.Root.Buckets[h]
		if len(bucket) != 0 {
			selection.Occurrences = make([]int, 0, len(bucket))
		}
		for _, entry := range bucket {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if entry.Position < 0 || entry.Position >= len(source.Committed) {
				return nil, fmt.Errorf("stuntgres: invalid hash candidate position")
			}
			row := source.Committed[entry.Position]
			if row.Row != entry.Row || row.Version != entry.Version {
				return nil, fmt.Errorf("stuntgres: invalid hash candidate identity")
			}
			ordinal := entry.Position
			if source.Visible != nil {
				var found bool
				ordinal, found = slices.BinarySearch(source.Visible, entry.Position)
				if !found {
					return nil, fmt.Errorf("stuntgres: incomplete hash candidate visibility")
				}
			}
			equal, err := capture.Root.Binding.Provider.Equal(ctx, entry.Key, value)
			if err != nil {
				return nil, err
			}
			if equal {
				selection.Occurrences = append(selection.Occurrences, ordinal)
			}
		}
		return selection, ctx.Err()
	}
	return nil, nil
}

// Each manifest entry is an SQL occurrence, even when physical identity repeats.
type Leaf struct {
	Ref              ids.ObjectRef
	Relation         *catalog.Relation
	Storage          *storage.Relation
	Only             bool
	Principal        authorization.PrincipalRef // Relation authority, not expression/session identity.
	FrozenAttributes []int32                    // Execution-owned stored-view subquery layout.
	Uses             []int
	TableOIDRead     bool // Requires whole-table SELECT; Uses stays dense ordinary indices.
	Mark             plan.RowMark
	Query            *parser.Select
	Table            *parser.TableReference
	// DELETE's immediate USING occurrences require reference-kind validation,
	// not SELECT FOR UPDATE authority or source tuple locks.
	DeleteReference  bool
	PartialCandidate *catalog.Relation
	AnalysisOnly     bool // Unreferenced CTE body: analyzed dependency, no executable ACL.
}

func SelectQuerySources(ctx context.Context, source *Source) error {
	selectOne := func(source *Source) error {
		if source.Inheritance != nil {
			return source.selectInheritancePrefixes(ctx)
		}
		if source.View == nil || source.RlsDenied {
			return ctx.Err()
		}
		data := source.View.Data()
		heap, err := visibility.ResolveRelationHeapAccess(data.Catalog, source.Ref)
		if err != nil {
			return err
		}
		_, err = SelectQuerySource(ctx, source, data.FrontierAt(source.Ref.Address), data.Ends[source.Ref.Address], data.Deleted[source.Ref.Address], heap)
		return err
	}
	if source.Sources == nil {
		if err := selectOne(source); err != nil {
			source.Close()
			return err
		}
		return nil
	}
	for _, leaf := range source.Sources {
		if err := selectOne(leaf); err != nil {
			source.Close()
			return err
		}
	}
	return nil
}
