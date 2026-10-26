package exec

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type RowCursor interface {
	Schema() []catalog.Column
	Next(context.Context, []sqltype.Value) (bool, error)
	Close() error
}

type RowResult interface {
	RowCursor
	RegclassText(sqltype.Value) (string, error)
	OutputEnvironment() sqltype.OutputEnvironment
}

func QueryPredicateRejectsAll(c *Context, expr expr.Expr) (bool, error) {
	value, known, err := plan.QueryPredicateProofValue(c, expr)
	if err != nil || !known {
		return false, err
	}
	truth, err := plan.TruthOf(value)
	return err == nil && truth != plan.True, err
}

// A leaf retains immutable relation-local prefixes; the first leaf also owns
// the occurrence bundle. Joined stages instead own flat admitted frames.
// A nil visible mapping preserves the allocation-free all-occurrences path.
type Source struct {
	View *visibility.View
	Ref  ids.ObjectRef
	// A frozen-view occurrence may carry an older immutable manifest ref.
	// Set only after contextual owner/provider proof at source capture.
	StoredRef        ids.ObjectRef
	Committed, Own   []storage.RowVersion
	PartialMask      *storage.IndexRoot
	Singleton        bool
	RlsDenied        bool
	RLS              *RLSProgram // Borrowed statement-owned per-occurrence USING program.
	NeedsBirthFilter bool        // Current TRUNCATE vector at a retained older frontier.
	Anonymous        [][]sqltype.Value
	PGSequences      *PGSequencesSource
	Foreign          *ForeignQuerySource
	Visible          []int
	HashIndexes      []CapturedHashIndex
	CatalogKeys      *CatalogKeyIndex // Indexes Anonymous of a shared catalog capture.
	Host             Host
	Authority        *Authority
	// Borrowed only by an opaque stored-source execution root after original
	// view lifetime/provider validation. This immutable definition supplies its
	// original manifest, never current rights or relation-owner substitution.
	RoutineRecipe *catalog.Relation
	// RoutineChecks borrows a stored view or validated frozen SQL action's
	// original manifest; a nil consumer does not turn an action into fresh SQL.
	RoutineChecks []catalog.ExpressionDependency
	Sources       []*Source
	// Both inheritance and partition trees use named-layout physical prefixes.
	Inheritance          []InheritanceQuerySegment
	InheritanceEnds      []int
	BranchPruner         QueryBranchPruner
	Flat                 []sqltype.Value
	FlatWidth, FlatCount int
	Materialized         bool
	Final                *FinalRows
	Input                *Source
	Layout               []int
	Scratch              []sqltype.Value
	Locks                *LockContext
	Lineage              *QueryLockLineage
	NestedRoot           plan.Node
	NestedInputs         *Source
	NestedStage          *Stage
	Inherited            plan.RowMark // Execution-only mark crossing this view boundary.
	// Locking and lazy virtual reads retain physical SQL occurrence identity.
	Occurrence               int
	Occurrences              []int
	Provenance               []int
	Versions                 []storage.RowVersion
	VersionValid             []bool // Nil: complete sequential cache; nonnil: sparse frontier.
	JoinLeft, JoinRight      *Source
	JoinPairs                []QueryPair
	invocationLineage        []queryInvocationLineage
	Window                   bool
	WindowCount, windowWidth int
	WindowValues             []sqltype.Value
	WindowIndices            []int
	// Presence uses packed flags only for demanded null-extended namespaces.
	// Slot lookup is immutable per source; emitted join rows own flat flags.
	PresenceSlots                  []int
	PresenceCount                  int
	PresenceConstant, presenceFlat []bool
}

func (s *Source) Count() int {
	if s.Window {
		return s.WindowCount
	}
	if s.Materialized {
		return s.FlatCount
	}
	if s.Final != nil {
		return s.Final.Count
	}
	if s.Input != nil {
		return s.Input.Count()
	}
	if s.Anonymous != nil {
		return len(s.Anonymous)
	}
	if s.Singleton {
		return 1
	}
	if s.Visible != nil {
		return len(s.Visible)
	}
	if s.Inheritance != nil {
		if len(s.InheritanceEnds) == 0 {
			return 0
		}
		return s.InheritanceEnds[len(s.InheritanceEnds)-1]
	}
	return len(s.Committed) + len(s.Own)
}

func (s *Source) Row(i int) []sqltype.Value {
	if i == AbsentQueryIndex {
		return nil
	}
	if s.Window {
		row := s.Input.Row(s.InputIndex(i))
		copy(s.Scratch, row)
		copy(s.Scratch[len(s.Scratch)-s.windowWidth:], s.WindowValues[i*s.windowWidth:(i+1)*s.windowWidth])
		return s.Scratch
	}
	if s.Materialized {
		if s.Anonymous != nil {
			return s.Anonymous[i]
		}
		return s.Flat[i*s.FlatWidth : (i+1)*s.FlatWidth]
	}
	if s.Input != nil {
		row := s.Input.Row(s.InputIndex(i))
		for j, slot := range s.Layout {
			if slot >= 0 {
				s.Scratch[j] = row[slot]
			}
		}
		return s.Scratch
	}
	if s.Final != nil {
		r := s.Final
		if r.Source != nil {
			index := i
			if r.Selection != nil {
				index = r.Selection[i]
			}
			row := r.Source.Row(index)
			for j, slot := range r.Slots {
				s.Scratch[j] = row[slot]
			}
			return s.Scratch
		}
		if r.Anonymous != nil {
			return r.Anonymous[i]
		}
		width := len(r.Columns)
		return r.Values[i*width : (i+1)*width]
	}
	if s.Anonymous != nil {
		return s.Anonymous[i]
	}
	if s.Singleton {
		return nil
	}
	if s.Inheritance != nil {
		segment, index := s.inheritanceSegment(i)
		return segment.MapValues(segment.Prefix.Row(index))
	}
	return s.VersionAt(s.RawIndex(i)).Values
}

func (s *Source) RawIndex(i int) int {
	if i == AbsentQueryIndex {
		return i
	}
	if s.Visible != nil {
		return s.Visible[i]
	}
	return i
}

func (s *Source) VersionAt(i int) storage.RowVersion {
	if i == AbsentQueryIndex {
		return storage.RowVersion{}
	}
	if s.Inheritance != nil {
		segment, index := s.inheritanceSegment(i)
		return segment.Prefix.VersionAt(segment.Prefix.RawIndex(index))
	}
	if i < len(s.Committed) {
		return s.Committed[i]
	}
	return s.Own[i-len(s.Committed)]
}

func (s *Source) Close() {
	s.RLS = nil // The statement, not this borrowed occurrence, owns the program.
	s.PGSequences = nil
	sources := s.Sources
	s.Sources = nil
	s.HashIndexes = nil
	s.CatalogKeys = nil
	s.BranchPruner = nil
	if s.NestedStage != nil {
		s.NestedStage.Close()
		s.NestedStage = nil
	}
	s.NestedRoot = nil
	if s.NestedInputs != nil {
		s.NestedInputs.Close()
		s.NestedInputs = nil
	}
	for _, child := range sources {
		if child != nil && child != s {
			child.Close()
		}
	}
	for _, segment := range s.Inheritance {
		segment.Prefix.Close()
	}
	s.Inheritance, s.InheritanceEnds = nil, nil
	s.Flat = nil
	s.WindowValues, s.WindowIndices = nil, nil
	s.PresenceSlots, s.PresenceConstant, s.presenceFlat = nil, nil, nil
	s.PresenceCount = 0
	s.WindowCount, s.windowWidth, s.Window = 0, 0, false
	s.Occurrences = nil
	s.Provenance = nil
	s.Versions = nil
	s.VersionValid = nil
	s.JoinLeft, s.JoinRight, s.JoinPairs = nil, nil, nil
	s.invocationLineage = nil
	s.FlatCount = 0
	s.FlatWidth = 0
	s.Materialized = false
	lineage := s.Lineage
	s.Lineage = nil
	stageClosed := lineage != nil && s.Final != nil && s.Final.Source == lineage.Stage
	if s.Final != nil {
		s.Final.Close()
		s.Final = nil
	}
	if lineage != nil {
		if !stageClosed {
			lineage.Stage.Close()
		}
		if lineage.Inputs != lineage.Stage {
			lineage.Inputs.Close()
		}
	}
	s.Locks = nil
	if s.Input != nil {
		s.Input.Close()
		s.Input = nil
	}
	s.Scratch, s.Layout = nil, nil
	if s.View != nil {
		s.View.Release()
	}
	s.View = nil
	s.Committed, s.Own = nil, nil
	s.Authority, s.Host = nil, nil
	s.PartialMask = nil
	s.Anonymous = nil
	s.Visible = nil
	s.RoutineRecipe, s.RoutineChecks = nil, nil
}

// SourceMode separates one-shot owners, rowstream capture owners whose scalar
// invocations borrow, and those borrowed invocations themselves. It is not the
// scroll/rewind lifetime of Stage.Retain.
type SourceMode uint8

const (
	SourceOwned SourceMode = iota
	SourceReusable
	SourceBorrowed
)

// Indexed stages retain occurrences, not copied input tuples. All scalar work
// delegates to the sole expression evaluator; each stage owns its private state.
type State struct {
	foreignParallelMaximum int // Only admitted eager top-level SELECT may set this.
	AsyncAppend            bool
	asyncCaptured          bool
	asyncInvocation        bool // Sticky lifetime exclusion, even when a child omits lexical frames.
	asyncFrontier          *foreignAppendFrontier
	Source                 *Source
	SourceMode             SourceMode
	Invocation             *Context // Borrowed same-query P+L frame; outer remains lexical.
	foreignOwner           any
	HashFilter             *plan.FilterNode
	Parameters             []sqltype.Value
	Input                  sqltype.InputEnvironment
	Output                 sqltype.OutputEnvironment
	Locking                bool
	IdentityReads          bool
	inherited              plan.RowMark
	Frame                  uint32
	Outer                  *Context
	Retain                 bool
	derived                map[*plan.DerivedNode]*Source
	Ctes                   *CTEInvocation
	hashOwner              *State
	Memberships            map[SubqueryMembershipKey]*subqueryMembership
	// Only the positive SELECT coordinator activates these on a physical leaf.
	ExecutionContext   context.Context
	ScanRange          bool
	ScanStart, scanEnd int
	aggregatePartials  *[]QueryGroup // Non-nil only inside the partial coordinator.
	aggregateAdmission *parallelAggregateAdmission
}

func (s *State) subquerySourceMode() SourceMode {
	if s.SourceMode == SourceOwned {
		return SourceOwned
	}
	return SourceBorrowed
}

// Physical child stages borrow the actual execution owner. Rescanning a parent
// does not change an uncorrelated child's inputs or its membership lifetime.
func (s *State) membershipOwner() *State {
	if s.hashOwner == nil {
		s.hashOwner = s
	}
	return s.hashOwner
}

func (s *State) ReleaseMemberships() {
	if s.hashOwner != nil && s.hashOwner != s {
		return
	}
	for _, membership := range s.Memberships {
		*membership = subqueryMembership{}
	}
	s.Memberships = nil
	s.hashOwner = nil
}

type Stage struct {
	Source             *Source
	Selection          []int
	HashCandidates     *hashCandidateSelection
	Permutation        []int
	OrderValues        []sqltype.Value
	CacheOccurrences   []int
	CacheWidth         int
	Pending            []queryPendingMark
	Columns            []catalog.Column
	Expressions        []expr.Expr
	Evaluation         Context
	Position           int
	Terminal           error
	Closed             bool
	Produce            func(context.Context, *Stage) (bool, error)
	Finish             func() error
	onFailure          func(error) error // Shared producers retain failures even before a reader pull.
	Done               bool
	Retain             bool
	Reset              func(context.Context, *Stage) (bool, error)
	scanRange          bool
	scanStart, scanEnd int
}

func (s Stage) Count() int {
	if s.Selection != nil {
		return len(s.Selection)
	}
	if s.scanRange {
		return s.scanEnd - s.scanStart
	}
	return s.Source.Count()
}

func (s Stage) Index(i int) int {
	if s.Permutation != nil {
		i = s.Permutation[i]
	}
	if s.Selection != nil {
		return s.Selection[i]
	}
	if s.scanRange {
		return s.scanStart + i
	}
	return i
}

func (s *Stage) Schema() []catalog.Column { return s.Columns }

func (s *Stage) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if s.Terminal != nil {
		return false, s.Terminal
	}
	if s.Closed {
		return false, nil
	}
	if len(dst) != len(s.Columns) {
		return false, s.fail(fmt.Errorf("stuntgres: invalid stage destination width"))
	}
	return s.NextProjected(ctx, dst)
}

// A nil destination discards values, not projection demand. Retained EXISTS
// shares this evaluator/validation seam without allocating an output tuple.
func (s *Stage) NextProjected(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if s.Terminal != nil {
		return false, s.Terminal
	}
	if s.Closed {
		return false, nil
	}
	fail := func(err error) (bool, error) { return false, s.fail(err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	present, err := s.ProjectOrdinal(ctx, s.Position, dst)
	if err != nil {
		return fail(err)
	}
	if !present && !s.Retain {
		if err := s.Close(); err != nil {
			return fail(err)
		}
	}
	if present {
		s.Position++
	}
	return present, nil
}

func (s *Stage) Rewind(ctx context.Context) (bool, error) {
	s.Position = 0
	if s.Reset != nil {
		changed, err := s.Reset(ctx, s)
		if err != nil {
			return false, s.fail(err)
		}
		return changed, nil
	}
	return false, nil
}

func (s *Stage) ProjectOrdinal(ctx context.Context, ordinal int, dst []sqltype.Value) (bool, error) {
	present, err := s.ensure(ctx, ordinal)
	if err != nil || !present {
		return present, err
	}
	row := s.Source.Row(s.Index(ordinal))
	if s.Expressions == nil {
		if dst != nil {
			copy(dst, row)
		}
	} else {
		s.Evaluation.setContext(ctx)
		s.Evaluation.Row = row
		s.Evaluation.BindQueryIdentity(s.Source, s.Index(ordinal))
		if s.CacheWidth != 0 {
			s.Evaluation.OrderValues = s.OrderRow(ordinal)
		}
		for i, expr := range s.Expressions {
			value, err := expr.Eval(&s.Evaluation)
			if err != nil {
				return false, s.fail(err)
			}
			if !value.ValidResult(s.Columns[i].Usage) {
				return false, s.fail(fmt.Errorf("stuntgres: invalid finalized query value"))
			}
			if dst != nil {
				dst[i] = value
			}
		}
	}
	return true, nil
}

func (s *Stage) release() {
	// The query execution owner, not this borrowed stage, owns the view pin.
	s.Source = nil
	s.Selection = nil
	s.HashCandidates = nil
	s.Expressions = nil
	s.Permutation = nil
	s.OrderValues = nil
	s.CacheOccurrences = nil
	s.Pending = nil
	s.Evaluation = Context{}
}

func (s *Stage) Close() error {
	if s.Closed {
		s.Terminal = nil
		return nil
	}
	var err error
	if s.Finish != nil {
		finish := s.Finish
		s.Finish = nil
		err = finish()
	}
	s.Produce = nil
	s.Reset = nil
	s.onFailure = nil
	s.release()
	s.Closed = true
	s.Terminal = nil
	return err
}

func (s *Stage) fail(err error) error {
	if err == errForeignPending {
		return err
	}
	if s.onFailure != nil {
		err = s.onFailure(err)
	}
	_ = s.Close()
	s.Terminal = err
	return err
}

func OpenNode(ctx context.Context, state *State, node plan.Node) (Stage, error) {
	state.CaptureAsyncAdmission()
	state.inheritCTEs()
	return openRetainedQueryNode(ctx, state, node)
}

func openRetainedQueryNode(ctx context.Context, state *State, node plan.Node) (out Stage, err error) {
	defer func() { out.Retain = state.Retain }()
	if err := ctx.Err(); err != nil {
		return Stage{}, err
	}
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		return OpenFunctionFromNode(ctx, state, n)
	case *plan.SetProjectNode:
		return OpenSetProjectNode(ctx, state, n)
	case *plan.WindowNode:
		return openWindowNode(ctx, state, n)
	case *plan.SetNode:
		return OpenSetNode(ctx, state, n)
	case *plan.RecursiveUnionNode:
		return openRecursiveUnion(ctx, state, n)
	case *plan.WorktableScanNode:
		return openWorktableScan(ctx, state, n)
	case *plan.LimitNode:
		return OpenLimitNode(ctx, state, n)
	case *plan.EvaluationNode:
		return OpenEvaluationNode(ctx, state, n)
	case *plan.RowLockNode:
		return openRowLockNode(ctx, state, n)
	case *plan.JoinNode:
		return OpenJoinNode(ctx, state, n)
	case *plan.DistinctNode:
		return openDistinctNode(ctx, state, n)
	case *plan.DistinctOnNode:
		return openDistinctOnNode(ctx, state, n)
	case *plan.DerivedNode:
		return OpenDerivedNode(ctx, state, n)
	case *plan.AggregateNode:
		return openAggregateNode(ctx, state, n)
	case *plan.ValuesNode:
		if state.Source == nil {
			return Stage{}, fmt.Errorf("stuntgres: invalid VALUES query source")
		}
		source := &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: state.Source.Authority}
		EstablishSourcePresence(source, n.PresenceSlot)
		out := Stage{Source: source, Columns: n.Schema(), Selection: []int{}}
		evaluation := state.Evaluation(nil)
		out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
			source.Flat, source.FlatCount = source.Flat[:0], 0
			out.Selection, out.Done = out.Selection[:0], false
			return true, ctx.Err()
		}
		out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
			i := source.FlatCount
			if i == len(n.Rows) {
				return false, nil
			}
			evaluation.setContext(ctx)
			for j, expr := range n.Rows[i] {
				value, err := expr.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				if err := ValidateProducedValue(value, n.Schema()[j].Usage); err != nil {
					return false, err
				}
				source.Flat = append(source.Flat, value)
			}
			out.Selection = append(out.Selection, i)
			source.FlatCount++
			return true, ctx.Err()
		}
		return out, nil
	case *plan.SingletonNode:
		if state.Source == nil {
			return Stage{}, fmt.Errorf("stuntgres: missing singleton statement source")
		}
		source := &Source{Singleton: true, Authority: state.Source.Authority, Host: state.Source.Host, Locks: state.Source.Locks}
		return Stage{Source: source, Columns: n.Schema()}, nil
	case *plan.SourceNode:
		source := state.Source
		if source != nil && source.Sources != nil {
			if n.Occurrence < 0 || n.Occurrence >= len(source.Sources) {
				return Stage{}, fmt.Errorf("stuntgres: invalid query occurrence")
			}
			source = source.Sources[n.Occurrence]
		}
		if source == nil || source.Ref != n.Ref && (source.StoredRef == (ids.ObjectRef{}) || source.StoredRef != n.Ref) {
			return Stage{}, fmt.Errorf("stuntgres: invalid query source target")
		}
		EstablishSourcePresence(source, n.PresenceSlot)
		if source.Foreign != nil {
			return OpenForeignStage(ctx, state, source, n)
		}
		if source.PGSequences != nil {
			return openPGSequencesStage(state, source, n.Schema()), nil
		}
		if source.Final != nil || source.Input != nil || source.NestedRoot != nil {
			return openSourceStage(ctx, state, source, n.Schema(), n)
		}
		if source.Anonymous == nil {
			if source.View == nil {
				return Stage{}, fmt.Errorf("stuntgres: missing query source")
			}
			data := source.View.Data()
			if source.Ref.Domain() != data.Catalog.Owner().Domain || source.Ref.Epoch != data.Catalog.Epoch() {
				return Stage{}, fmt.Errorf("stuntgres: invalid query source target")
			}
		}
		child := Stage{Source: source, Columns: n.Schema(), scanRange: state.ScanRange, scanStart: state.ScanStart, scanEnd: state.scanEnd}
		if source.RLS != nil {
			return openRowSecurityStage(ctx, state, child), nil
		}
		return child, nil
	case *plan.FilterNode:
		evaluation := state.Evaluation(ctx)
		rejects, err := QueryPredicateRejectsAll(&evaluation, n.Predicate)
		if err != nil {
			return Stage{}, err
		}
		// HAVING rejection follows ordered-set finalization, including direct
		// side effects/errors on empty global input. This is a local aggregate
		// frontier, not a reason to force unrelated derived/WHERE producers.
		finalizesOrderedSet := false
		if aggregate, ok := n.Child.(*plan.AggregateNode); ok {
			for _, call := range aggregate.Calls {
				if _, ordered := call.Provider.(functions.OrderedAggregate); ordered {
					finalizesOrderedSet = true
					break
				}
			}
		}
		if rejects && !finalizesOrderedSet && !QueryHasMandatoryPhysicalFrontier(n.Child, state.Source) {
			// Only child-reachable consumed physical marks require the eager
			// input frontier. Unrelated semantic context, identity-only manifests
			// and unopened opaque CTE bodies do not demand this child.
			source := &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host, Locks: state.Source.Locks}
			return Stage{Source: source, Columns: n.Schema()}, nil
		}
		if join, ok := n.Child.(*plan.JoinNode); ok && !QueryHasMandatoryPhysicalFrontier(n.Child, state.Source) {
			// A function right input always admits its predecessor rows
			// first. Other inputs do so only in plain reads: locking and
			// identity reads keep the post-join filter order.
			_, function := join.Right.(*plan.FunctionFromNode)
			if function || !retainsQueryIdentity(state) {
				if restricted := plan.InnerJoinRestriction(join, n.Predicate); restricted != nil {
					// Keep canonical/view literal paths intact. Only this
					// execution filters left rows before the join. EPQ instead
					// retains its captured, identity-keyed topology even after
					// its physical locking frontier has been cleared.
					return OpenNode(ctx, state, restricted)
				}
			}
		}
		AdmitPGSequencesPredicate(n.Child, state.Source, n.Predicate, 0, evaluation.Frame)
		child, err := OpenNode(ctx, state, n.Child)
		if err != nil {
			return Stage{}, err
		}
		branches, err := selectQueryBranches(ctx, state, n, &child)
		if err != nil {
			child.Close()
			return Stage{}, err
		}
		var candidates *hashCandidateSelection
		keyed := false
		if !branches.selection.Applied {
			candidates, err = hashFilterCandidates(ctx, state, n, child.Source)
			if err != nil {
				child.Close()
				return Stage{}, err
			}
			if candidates == nil {
				candidates = catalogKeyCandidates(ctx, state, n, &child)
				keyed = candidates != nil
			}
		}
		out := ProducingStage(&child, n.Schema())
		out.HashCandidates = candidates
		baseReset := out.Reset
		out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
			changed, err := baseReset(ctx, out)
			if err != nil {
				return false, err
			}
			if changed {
				branches, err = selectQueryBranches(ctx, state, n, &child)
				if err != nil {
					return false, err
				}
				// Catalog key positions belong to the child's previous rows.
				if branches.selection.Applied || keyed {
					candidates, out.HashCandidates = nil, nil
				}
			} else {
				branches.position = 0
			}
			if n.Predicate.Properties().Volatility == expr.Volatile {
				if !changed {
					if _, err := child.Rewind(ctx); err != nil {
						return false, err
					}
				}
				out.Selection, out.CacheOccurrences, out.Done = out.Selection[:0], out.CacheOccurrences[:0], false
				if out.HashCandidates != nil {
					out.HashCandidates.Position = 0
				}
				return true, nil
			}
			return changed, nil
		}
		out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
			for {
				var i int
				if candidates := out.HashCandidates; candidates != nil {
					if candidates.Position == len(candidates.Occurrences) {
						return false, ctx.Err()
					}
					i = candidates.Occurrences[candidates.Position]
					candidates.Position++
				} else {
					var present bool
					var err error
					i, present, err = branches.next(ctx, &child)
					if err != nil || !present {
						return present, err
					}
				}
				if mask := child.Source.PartialMask; mask != nil {
					row := child.Source.VersionAt(child.Source.RawIndex(child.Index(i)))
					if !PartialMaskContains(mask, row) {
						continue
					}
				}
				evaluation.setContext(ctx)
				evaluation.BindQueryRow(child.Source, child.Index(i))
				value, err := n.Predicate.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				truth, err := plan.TruthOf(value)
				if err != nil {
					return false, err
				}
				if truth == plan.True {
					RetainOccurrence(out, &child, i)
					return true, ctx.Err()
				}
			}
		}
		return out, nil
	case *plan.SortNode:
		if n.PresenceOnly {
			return OpenNode(ctx, state, n.Child)
		}
		child, err := OpenNode(ctx, state, n.Child)
		if err != nil {
			return Stage{}, err
		}
		child, err = SettlePendingQueryMarks(ctx, state, n.Child, child)
		if err != nil {
			return Stage{}, err
		}
		transferred := false
		defer func() {
			if !transferred {
				child.Close()
			}
		}()
		if err := child.Drain(ctx); err != nil {
			return Stage{}, err
		}
		if child.Selection == nil {
			child.Selection = make([]int, child.Count())
			for i := range child.Selection {
				child.Selection[i] = i
				if child.scanRange {
					child.Selection[i] += child.scanStart
				}
			}
		}
		if n.CacheWidth != 0 {
			child.CacheWidth = n.CacheWidth
			child.CacheOccurrences = nil
			child.OrderValues = make([]sqltype.Value, child.Count()*n.CacheWidth)
			child.Permutation = make([]int, child.Count())
			evaluation := state.Evaluation(ctx)
			for i := range child.Count() {
				child.Permutation[i] = i
				evaluation.BindQueryRow(child.Source, child.Selection[i])
				for _, key := range n.Keys {
					if err := ctx.Err(); err != nil {
						return Stage{}, err
					}
					if key.Cache < 0 || child.OrderValues[i*n.CacheWidth+key.Cache].Type() != nil {
						continue
					}
					value, err := key.Expression.Eval(&evaluation)
					if err != nil {
						return Stage{}, err
					}
					if value.Type() != key.Expression.ResultUsage().Type {
						return Stage{}, fmt.Errorf("stuntgres: invalid ordering value")
					}
					child.OrderValues[i*n.CacheWidth+key.Cache] = value
					if err := ctx.Err(); err != nil {
						return Stage{}, err
					}
				}
			}
			for i := range child.Count() {
				evaluation.BindQueryRow(child.Source, child.Selection[i])
				for j, expr := range n.Outputs {
					value, err := expr.Eval(&evaluation)
					if err != nil {
						return Stage{}, err
					}
					child.OrderValues[i*n.CacheWidth+n.CacheWidth-len(n.Outputs)+j] = value
				}
			}
		}
		indices := child.Selection
		if child.Permutation != nil {
			indices = child.Permutation
		}
		err = MergeQueryIndices(ctx, indices, func(a, b int) (int, error) {
			leftIndex, rightIndex := a, b
			var leftValues, rightValues []sqltype.Value
			if child.Permutation != nil {
				leftIndex, rightIndex = child.Selection[a], child.Selection[b]
				leftValues = child.OrderValues[a*n.CacheWidth : (a+1)*n.CacheWidth]
				rightValues = child.OrderValues[b*n.CacheWidth : (b+1)*n.CacheWidth]
			}
			return CompareQueryOrder(ctx, n.Keys, state.Input,
				QuerySortRow{Source: child.Source, Index: leftIndex, values: leftValues},
				QuerySortRow{Source: child.Source, Index: rightIndex, values: rightValues})
		})
		transferred = err == nil
		child.Reset = nil
		return child, err
	case *plan.ProjectNode:
		if n.RuleInvocation {
			local := *state
			local.Outer = state.Invocation
			local.inheritAsyncAdmission(state)
			state = &local
		}
		if !state.IdentityReads {
			state.IdentityReads = QueryHasIdentityReads(n)
		}
		var owner *CTEInvocation
		if n.CTEOwner {
			state.membershipOwner()
			owner = &CTEInvocation{Frame: n.Frame, Parent: state.Ctes, State: *state}
			owner.State.inheritAsyncAdmission(state)
			state.Ctes = owner
		}
		if n.Frame != 0 {
			state.Frame = n.Frame
		}
		child, err := OpenNode(ctx, state, n.Child)
		if err != nil {
			if owner != nil {
				_ = owner.Close()
			}
			return Stage{}, err
		}
		child.Columns = n.Schema()
		child.Expressions = n.Expressions
		child.Evaluation = state.Evaluation(nil)
		if owner != nil {
			finish := child.Finish
			child.Finish = func() error {
				var first error
				if finish != nil {
					first = finish()
				}
				if failure := owner.Close(); first == nil {
					first = failure
				}
				return first
			}
		}
		return child, nil
	default:
		return Stage{}, fmt.Errorf("stuntgres: invalid logical query node")
	}
}

// Unlike a bool sort callback, every comparison can stop production with its
// original error. One scratch buffer serves every merge level.
func MergeQueryIndices(ctx context.Context, indices []int, compare func(int, int) (int, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(indices) < 2 {
		return nil
	}
	scratch := make([]int, len(indices))
	var merge func(int, int) error
	merge = func(lo, hi int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if hi-lo < 2 {
			return nil
		}
		mid := lo + (hi-lo)/2
		if err := merge(lo, mid); err != nil {
			return err
		}
		if err := merge(mid, hi); err != nil {
			return err
		}
		i, j, k := lo, mid, lo
		for i < mid && j < hi {
			if err := ctx.Err(); err != nil {
				return err
			}
			order, err := compare(indices[i], indices[j])
			if err != nil {
				return err
			}
			if order <= 0 {
				scratch[k] = indices[i]
				i++
			} else {
				scratch[k] = indices[j]
				j++
			}
			k++
		}
		k += copy(scratch[k:hi], indices[i:mid])
		copy(scratch[k:hi], indices[j:hi])
		copy(indices[lo:hi], scratch[lo:hi])
		return ctx.Err()
	}
	return merge(0, len(indices))
}

// FinalRows contains only finalized output, never expressions or parameters.
// self prevents forged or copied handles from releasing another cursor's pins.
type FinalRows struct {
	Self               *FinalRows
	Columns            []catalog.Column
	Source             *Source
	owner              *Source // Capture tree when projection retains a non-root occurrence.
	Selection, Slots   []int
	Values             []sqltype.Value
	RegclassOutput     map[string]string
	RegtypeOutput      map[uint32]string
	RegprocOutput      map[uint32]string
	RegprocedureOutput map[uint32]string
	RegroleOutput      map[uint32]string
	OperatorOutput     map[OperatorOutputKey]string
	OperatorGroups     OperatorGroups
	RegobjectOutput    map[ids.ObjectAddress]string
	RegTSOutput        map[RegTSOutputKey]string
	ACLAuthority       *sqltype.ACLAuthority
	AclitemOutput      map[uint32]string
	Anonymous          [][]sqltype.Value
	Count, Position    int
	terminal           error
	exhausted, Closed  bool
	Output             sqltype.OutputEnvironment
}

func NewFinalRows(schema []catalog.Column, count int) *FinalRows {
	r := &FinalRows{Columns: schema, Count: count}
	r.Self = r
	return r
}

func (r *FinalRows) Schema() []catalog.Column { return r.Columns }

func (r *FinalRows) OutputEnvironment() sqltype.OutputEnvironment { return r.Output }

func (r *FinalRows) RegclassText(v sqltype.Value) (string, error) {
	key, err := v.Text()
	if err != nil {
		return "", err
	}
	text, ok := r.RegclassOutput[key]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
	}
	return text, nil
}

func (r *FinalRows) FormatRegclass(id uint32) (string, error) {
	text, ok := r.RegclassOutput[strconv.FormatUint(uint64(id), 10)]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
	}
	return text, nil
}

func (r *FinalRows) release() {
	if r.owner != nil {
		r.owner.Close()
		r.owner = nil
	} else if r.Source != nil {
		r.Source.Close()
	}
	r.Source = nil
	r.Selection, r.Slots, r.Values = nil, nil, nil
	r.Anonymous = nil
	r.RegclassOutput = nil
	r.RegtypeOutput = nil
	r.RegprocOutput = nil
	r.RegroleOutput = nil
	r.OperatorOutput = nil
	r.OperatorGroups = nil
	r.RegprocedureOutput = nil
	r.RegobjectOutput = nil
	r.RegTSOutput = nil
	r.AclitemOutput = nil
	r.ACLAuthority = nil
}

func (r *FinalRows) fail(err error) (bool, error) {
	r.terminal = err
	r.release()
	return false, err
}

func (r *FinalRows) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if r == nil || r.Self != r {
		return false, fmt.Errorf("stuntgres: invalid result owner")
	}
	if r.Closed || r.exhausted {
		return false, nil
	}
	if r.terminal != nil {
		return false, r.terminal
	}
	if err := ctx.Err(); err != nil {
		return r.fail(err)
	}
	if len(dst) != len(r.Columns) {
		return r.fail(fmt.Errorf("stuntgres: invalid result destination width"))
	}
	if r.Position == r.Count {
		r.exhausted = true
		r.release()
		return false, nil
	}
	if r.Source != nil {
		index := r.Position
		if r.Selection != nil {
			index = r.Selection[index]
		}
		row := r.Source.Row(index)
		for i, slot := range r.Slots {
			dst[i] = row[slot]
		}
	} else {
		if r.Anonymous != nil {
			copy(dst, r.Anonymous[r.Position])
		} else {
			copy(dst, r.Values[r.Position*len(dst):(r.Position+1)*len(dst)])
		}
	}
	r.Position++
	return true, nil
}

func (r *FinalRows) Close() error {
	if r != nil && r.Self == r && !r.Closed {
		r.release()
		r.terminal = nil
		r.Closed = true
	}
	return nil
}

// A command sink takes already-finalized values, never evaluating expressions
// again. Owned flat values transfer directly; a borrowed projection is copied
// once to sever its source lifetime and slot mapping before catalog publication.
func (r *FinalRows) TakeTableRows(ctx context.Context) (rows [][]sqltype.Value, err error) {
	if r == nil || r.Self != r || r.Closed || r.Position != 0 {
		return nil, fmt.Errorf("stuntgres: invalid query table result owner")
	}
	defer func() {
		closeErr := r.Close()
		if err == nil {
			err = closeErr
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if r.Anonymous != nil {
		rows = r.Anonymous
		r.Anonymous = nil
		return rows, nil
	}
	rows = make([][]sqltype.Value, r.Count)
	width := len(r.Columns)
	values := r.Values
	if r.Source != nil {
		values = make([]sqltype.Value, r.Count*width)
		for i := range rows {
			row := values[i*width : (i+1)*width]
			ok, nextErr := r.Next(ctx, row)
			if nextErr != nil {
				return nil, nextErr
			}
			if !ok {
				return nil, fmt.Errorf("stuntgres: truncated query table result")
			}
		}
	} else {
		r.Values = nil
	}
	for i := range rows {
		rows[i] = values[i*width : (i+1)*width]
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// SELECT and INSERT settle residual qualified marks at the eager publication
// frontier. A nested scalar bridge is not a publication frontier.
func OpenQueryProjectStage(ctx context.Context, state *State, project *plan.ProjectNode) (Stage, error) {
	child, err := OpenNode(ctx, state, project)
	if err != nil {
		return Stage{}, state.FinishSources(err)
	}
	settled, err := SettlePendingQueryMarks(ctx, state, project.Child, child)
	if err != nil {
		_ = child.Close()
		return Stage{}, state.FinishSources(err)
	}
	if state.SourceMode != SourceBorrowed {
		finish := settled.Finish
		settled.Finish = func() error {
			var failure error
			if finish != nil {
				failure = finish()
			}
			return state.FinishSources(failure)
		}
	}
	return settled, nil
}

func Finalize(ctx context.Context, root plan.Node, source *Source, parameters []sqltype.Value) (RowResult, int, error) {
	return finalizeQuery(ctx, root, source, parameters, 0)
}

func finalizeQuery(ctx context.Context, root plan.Node, source *Source, parameters []sqltype.Value, foreignMaximum int) (RowResult, int, error) {
	keep := false
	defer func() {
		_ = FinishForeignSources(ctx, source)
		if !keep {
			source.Close()
		}
	}()
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return nil, 0, fmt.Errorf("stuntgres: query lacks final projection")
	}
	state := &State{Source: source, SourceMode: SourceReusable, Parameters: parameters, Locking: source.Locks != nil, HashFilter: HashQueryFilter(root)}
	if InParallelOperation(ctx) {
		state.ExecutionContext = ctx
	}
	state.foreignParallelMaximum = foreignMaximum
	defer state.ReleaseMemberships()
	if host := source.LiveHost(); host != nil {
		state.Input, state.Output = host.Environments()
	}
	stage, err := OpenQueryProjectStage(ctx, state, project)
	if err != nil {
		return nil, 0, err
	}
	defer stage.Close()
	if err := stage.Drain(ctx); err != nil {
		return nil, 0, err
	}
	r := NewFinalRows(project.Schema(), stage.Count())
	r.Output = state.Output
	var slots []int
	direct := true
	frame := project.Frame
	if frame == 0 {
		frame = 1
	}
	for _, expr := range project.Expressions {
		column, ok := expr.(plan.ColumnExpr)
		if !ok || column.Virtual != nil {
			direct = false
			break
		}
		if column.Scope != 1 || column.Frame != frame || column.Slot < 0 || column.Slot >= len(project.Child.Schema()) {
			return nil, 0, fmt.Errorf("stuntgres: invalid query projection frame")
		}
	}
	width := len(project.Schema())
	if width != len(project.Expressions) {
		return nil, 0, fmt.Errorf("stuntgres: invalid query projection width")
	}
	if !direct {
		r.Values = make([]sqltype.Value, r.Count*width)
	}
	if direct {
		slots = make([]int, len(project.Expressions))
		for i, expr := range project.Expressions {
			slots[i] = expr.(plan.ColumnExpr).Slot
		}
		evaluation := state.Evaluation(ctx)
		for i := range r.Count {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			evaluation.BindQueryRow(stage.Source, stage.Index(i))
			for j, expr := range project.Expressions {
				value, err := expr.Eval(&evaluation)
				if err != nil {
					return nil, 0, err
				}
				if !value.ValidResult(r.Columns[j].Usage) {
					return nil, 0, fmt.Errorf("stuntgres: invalid finalized query value")
				}
			}
		}
	} else {
		for i := range r.Count {
			present, err := stage.Next(ctx, r.Values[i*width:(i+1)*width])
			if err != nil {
				return nil, 0, err
			}
			if !present {
				return nil, 0, fmt.Errorf("stuntgres: early projection EOF")
			}
		}
	}
	selection := stage.Selection
	if stage.Permutation != nil && direct {
		// Final physical output identity is distinct from sort-cache identity.
		selection = stage.Permutation
		if stage.Selection != nil {
			for i, occurrence := range selection {
				selection[i] = stage.Selection[occurrence]
			}
		}
	}
	stageSource := stage.Source
	if err := stage.Close(); err != nil {
		return nil, 0, err
	}
	if err := FinishForeignSources(ctx, source); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if direct && r.Count > 0 {
		r.Source, r.Selection, r.Slots = stageSource, selection, slots
		keep = true
		if stageSource != source && !querySourceRetains(stageSource, source) {
			if querySourceRetains(source, stageSource) {
				r.owner = source
				detachQuerySource(source)
			} else {
				source.Close()
			}
		}
	}
	if !direct || r.Count == 0 {
		stageSource.Close()
	}
	detachQuerySource(stageSource)
	return r, r.Count, nil
}

func querySourceRetains(source, target *Source) bool {
	if source == target {
		return true
	}
	if source == nil {
		return false
	}
	if querySourceRetains(source.Input, target) {
		return true
	}
	for _, child := range source.Sources {
		if child != source && querySourceRetains(child, target) {
			return true
		}
	}
	return false
}
