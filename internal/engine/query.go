package engine

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

func queryPredicateRejectsAll(c *EvalContext, expr expr.Expr) (bool, error) {
	value, known, err := plan.QueryPredicateProofValue(c, expr)
	if err != nil || !known {
		return false, err
	}
	truth, err := plan.TruthOf(value)
	return err == nil && truth != plan.True, err
}

// preflightQuery retains schemas and source identity from analysis. Only paths
// containing folded coercions are copied for this execution.
func preflightQuery(c *EvalContext, node plan.Node) (plan.Node, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		return preflightFunctionFrom(c, n)
	case *plan.SetProjectNode:
		return preflightQuerySet(c, n)
	case *plan.WindowNode:
		return rewriteQueryExpressions(n, func(expr expr.Expr) (expr.Expr, bool, error) { return preflightCoercions(c, expr) }, func(child plan.Node) (plan.Node, error) {
			if child == n {
				return child, nil
			}
			return preflightQuery(c, child)
		})
	case *plan.SetNode:
		return preflightSet(c, n)
	case *plan.RecursiveUnionNode:
		bound, err := preflightSet(c, &n.SetNode)
		if err != nil {
			return nil, err
		}
		copy := *n
		copy.SetNode = *bound.(*plan.SetNode)
		if n.Cycle != nil {
			recipe := *n.Cycle
			recipe.Mark, _, err = preflightCoercions(c, recipe.Mark)
			if err != nil {
				return nil, err
			}
			recipe.Fallback, _, err = preflightCoercions(c, recipe.Fallback)
			if err != nil {
				return nil, err
			}
			copy.Cycle = &recipe
		}
		return &copy, nil
	case *plan.WorktableScanNode:
		return n, nil
	case *plan.DistinctNode:
		return preflightDistinct(c, n)
	case *plan.DistinctOnNode:
		return preflightDistinctOn(c, n)
	case *plan.DerivedNode:
		// Ordinary queries retain their runtime projection frontier. Rule
		// programs prepare only the child payload retained by producer demand.
		if !c.planStaticOperators || n.Query == nil {
			return n, nil
		}
		root, err := preflightQuery(c, n.Query.Root)
		if err != nil {
			return nil, err
		}
		if root == n.Query.Root {
			return n, nil
		}
		copy, query := *n, *n.Query
		query.Root = root
		copy.Query = &query
		return &copy, nil
	case *plan.LimitNode:
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		// Bounds retain their ordered runtime coercion and value-check frontier.
		// Folding COUNT here could overtake a failing OFFSET.
		if child != n.Child {
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		return n, nil
	case *plan.EvaluationNode:
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		var expressions []expr.Expr
		for i, operand := range n.Expressions {
			folded, changed, err := preflightCoercions(c, operand)
			if err != nil {
				return nil, err
			}
			if owned, did := queryScalarOwner(child, folded); did {
				folded, changed = owned, true
			}
			if changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = folded
			}
		}
		if child != n.Child || expressions != nil {
			copy := *n
			copy.Child = child
			if expressions != nil {
				copy.Expressions = expressions
			}
			return &copy, nil
		}
		return n, nil
	case *plan.RowLockNode:
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if child != n.Child {
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		return n, nil
	case *plan.JoinNode:
		return preflightJoin(c, n)
	case *plan.SourceNode, *plan.SingletonNode:
		return n, nil
	case *plan.ValuesNode:
		var rows [][]expr.Expr
		for i, row := range n.Rows {
			var expressions []expr.Expr
			for j, expression := range row {
				folded, changed, err := preflightCoercions(c, expression)
				if err != nil {
					return nil, err
				}
				if changed {
					if expressions == nil {
						expressions = append([]expr.Expr(nil), row...)
					}
					expressions[j] = folded
				}
			}
			if expressions != nil {
				if rows == nil {
					rows = append([][]expr.Expr(nil), n.Rows...)
				}
				rows[i] = expressions
			}
		}
		if rows != nil {
			copy := *n
			copy.Rows = rows
			return &copy, nil
		}
	case *plan.AggregateNode:
		if n.PresenceOnly {
			child, err := preflightQuery(c, n.Child)
			if err != nil {
				return nil, err
			}
			if child == n.Child {
				return n, nil
			}
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		var keys []expr.Expr
		for i, key := range n.Keys {
			folded, replacement, err := preflightCoercions(c, key)
			if err != nil {
				return nil, err
			}
			if replacement {
				if keys == nil {
					keys = append([]expr.Expr(nil), n.Keys...)
				}
				keys[i] = folded
			}
		}
		var calls []plan.AggregateCall
		for i, call := range n.Calls {
			// Ordered-set direct expressions remain group-finalization owned:
			// preflight must not evaluate them for absent keyed groups.
			for j, expression := range [3]expr.Expr{call.Argument, call.Separator, call.Filter} {
				if expression == nil {
					continue
				}
				folded, replacement, err := preflightCoercions(c, expression)
				if err != nil {
					return nil, err
				}
				if replacement {
					if calls == nil {
						calls = append([]plan.AggregateCall(nil), n.Calls...)
					}
					if j == 0 {
						calls[i].Argument = folded
					} else if j == 1 {
						calls[i].Separator = folded
					} else {
						calls[i].Filter = folded
					}
				}
			}
			var order []plan.SortKey
			for k, key := range call.Order {
				folded, replacement, err := preflightCoercions(c, key.Expression)
				if err != nil {
					return nil, err
				}
				if replacement {
					if order == nil {
						order = append([]plan.SortKey(nil), call.Order...)
					}
					order[k].Expression = folded
				}
			}
			if order != nil {
				if calls == nil {
					calls = append([]plan.AggregateCall(nil), n.Calls...)
				}
				calls[i].Order = order
			}
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if keys != nil || calls != nil || child != n.Child {
			copy := *n
			copy.Child = child
			if keys != nil {
				copy.Keys = keys
			}
			if calls != nil {
				copy.Calls = calls
			}
			return &copy, nil
		}
	case *plan.FilterNode:
		predicate, changed, err := preflightCoercions(c, n.Predicate)
		if err != nil {
			return nil, err
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if changed || child != n.Child {
			copy := *n
			copy.Child, copy.Predicate = child, predicate
			return &copy, nil
		}
	case *plan.SortNode:
		if n.PresenceOnly {
			child, err := preflightQuery(c, n.Child)
			if err != nil {
				return nil, err
			}
			if child == n.Child {
				return n, nil
			}
			copy := *n
			copy.Child = child
			return &copy, nil
		}
		var keys []plan.SortKey
		for i, key := range n.Keys {
			if key.Expression.CollationIndeterminate() {
				return nil, setOrderCollationError()
			}
			folded, changed, err := preflightCoercions(c, key.Expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if keys == nil {
					keys = append([]plan.SortKey(nil), n.Keys...)
				}
				keys[i].Expression = folded
			}
		}
		var outputs []expr.Expr
		for i, operand := range n.Outputs {
			folded, changed, err := preflightCoercions(c, operand)
			if err != nil {
				return nil, err
			}
			if changed {
				if outputs == nil {
					outputs = append([]expr.Expr(nil), n.Outputs...)
				}
				outputs[i] = folded
			}
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		if child != n.Child || keys != nil || outputs != nil {
			copy := *n
			copy.Child = child
			if keys != nil {
				copy.Keys = keys
			}
			if outputs != nil {
				copy.Outputs = outputs
			}
			return &copy, nil
		}
	case *plan.ProjectNode:
		// Projection initialization precedes opening its child. CHECK recipes
		// may fail here, but checked conversion truth stays in each runtime row.
		var expressions []expr.Expr
		for i, expression := range n.Expressions {
			folded, changed, err := preflightCoercions(c, expression)
			if err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = folded
			}
		}
		if expressions != nil {
			copy := *n
			copy.Expressions = expressions
			n, node = &copy, &copy
			expressions = nil
		}
		pruned, err := pruneProjectWindows(n)
		if err != nil {
			return nil, err
		}
		n, node = pruned, pruned
		if c.planStaticOperators {
			// Use surviving scalar recipes, not dead conditional branches, to
			// choose child payload demand. Scalar preparation keeps its usual
			// precedence over opening/preparing the source.
			demanded, err := ruleQueryProducerDemand(n, nil, 0)
			if err != nil {
				return nil, err
			}
			n, node = demanded.(*plan.ProjectNode), demanded
		}
		child, err := preflightQuery(c, n.Child)
		if err != nil {
			return nil, err
		}
		for i, expression := range n.Expressions {
			if expressions != nil {
				expression = expressions[i]
			}
			if owned, changed := queryScalarOwner(child, expression); changed {
				if expressions == nil {
					expressions = append([]expr.Expr(nil), n.Expressions...)
				}
				expressions[i] = owned
			}
		}
		if expressions != nil || child != n.Child {
			copy := *n
			copy.Child = child
			if expressions != nil {
				copy.Expressions = expressions
			}
			return &copy, nil
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid logical query node")
	}
	return node, nil
}

// A leaf retains immutable relation-local prefixes; the first leaf also owns
// the occurrence bundle. Joined stages instead own flat admitted frames.
// A nil visible mapping preserves the allocation-free all-occurrences path.
type querySource struct {
	view *visibility.View
	ref  ids.ObjectRef
	// A frozen-view occurrence may carry an older immutable manifest ref.
	// Set only after contextual owner/provider proof at source capture.
	storedRef        ids.ObjectRef
	committed, own   []storage.RowVersion
	partialMask      *storage.IndexRoot
	singleton        bool
	rlsDenied        bool
	needsBirthFilter bool // Current TRUNCATE vector at a retained older frontier.
	anonymous        [][]sqltype.Value
	pgSequences      *pgSequencesSource
	foreign          *foreignQuerySource
	visible          []int
	hashIndexes      []capturedHashIndex
	host             execHost
	authority        *queryAuthority
	// Borrowed only by an opaque stored-source execution root after original
	// view lifetime/provider validation. This immutable definition supplies its
	// original manifest, never current rights or relation-owner substitution.
	routineRecipe *catalog.Relation
	// routineChecks is routineRecipe's captured expression manifest.
	routineChecks []catalog.ExpressionDependency
	sources       []*querySource
	// Both inheritance and partition trees use named-layout physical prefixes.
	inheritance          []inheritanceQuerySegment
	inheritanceEnds      []int
	flat                 []sqltype.Value
	flatWidth, flatCount int
	materialized         bool
	final                *finalRows
	input                *querySource
	layout               []int
	scratch              []sqltype.Value
	locks                *queryLockContext
	lineage              *queryLockLineage
	nestedRoot           plan.Node
	nestedInputs         *querySource
	nestedStage          *indexedStage
	inherited            plan.RowMark // Execution-only mark crossing this view boundary.
	// Locking and lazy virtual reads retain physical SQL occurrence identity.
	occurrence               int
	occurrences              []int
	provenance               []int
	versions                 []storage.RowVersion
	versionValid             []bool // Nil: complete sequential cache; nonnil: sparse frontier.
	joinLeft, joinRight      *querySource
	joinPairs                []queryPair
	invocationLineage        []queryInvocationLineage
	window                   bool
	windowCount, windowWidth int
	windowValues             []sqltype.Value
	windowIndices            []int
	// Presence uses packed flags only for demanded null-extended namespaces.
	// Slot lookup is immutable per source; emitted join rows own flat flags.
	presenceSlots                  []int
	presenceCount                  int
	presenceConstant, presenceFlat []bool
}

func (e *Engine) captureSequenceSourceLocked(scope *StatementScope, ref ids.ObjectRef, relation *catalog.Relation) (*querySource, error) {
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return nil, fmt.Errorf("stuntgres: sequence state missing")
	}
	i := relation.Columns[0].Usage.Type
	last, _ := sqltype.Integer(i, g.Last)
	log, _ := sqltype.Integer(i, 0)
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{{last, log, sqltype.Boolean(relation.Columns[2].Usage.Type, g.Called)}}}, nil
}
func (s *querySource) count() int {
	if s.window {
		return s.windowCount
	}
	if s.materialized {
		return s.flatCount
	}
	if s.final != nil {
		return s.final.count
	}
	if s.input != nil {
		return s.input.count()
	}
	if s.anonymous != nil {
		return len(s.anonymous)
	}
	if s.singleton {
		return 1
	}
	if s.visible != nil {
		return len(s.visible)
	}
	if s.inheritance != nil {
		if len(s.inheritanceEnds) == 0 {
			return 0
		}
		return s.inheritanceEnds[len(s.inheritanceEnds)-1]
	}
	return len(s.committed) + len(s.own)
}
func (s *querySource) row(i int) []sqltype.Value {
	if i == absentQueryIndex {
		return nil
	}
	if s.window {
		row := s.input.row(s.inputIndex(i))
		copy(s.scratch, row)
		copy(s.scratch[len(s.scratch)-s.windowWidth:], s.windowValues[i*s.windowWidth:(i+1)*s.windowWidth])
		return s.scratch
	}
	if s.materialized {
		if s.anonymous != nil {
			return s.anonymous[i]
		}
		return s.flat[i*s.flatWidth : (i+1)*s.flatWidth]
	}
	if s.input != nil {
		row := s.input.row(s.inputIndex(i))
		for j, slot := range s.layout {
			if slot >= 0 {
				s.scratch[j] = row[slot]
			}
		}
		return s.scratch
	}
	if s.final != nil {
		r := s.final
		if r.source != nil {
			index := i
			if r.selection != nil {
				index = r.selection[i]
			}
			row := r.source.row(index)
			for j, slot := range r.slots {
				s.scratch[j] = row[slot]
			}
			return s.scratch
		}
		if r.anonymous != nil {
			return r.anonymous[i]
		}
		width := len(r.schema)
		return r.values[i*width : (i+1)*width]
	}
	if s.anonymous != nil {
		return s.anonymous[i]
	}
	if s.singleton {
		return nil
	}
	if s.inheritance != nil {
		segment, index := s.inheritanceSegment(i)
		return segment.mapValues(segment.prefix.row(index))
	}
	return s.versionAt(s.rawIndex(i)).Values
}
func (s *querySource) rawIndex(i int) int {
	if i == absentQueryIndex {
		return i
	}
	if s.visible != nil {
		return s.visible[i]
	}
	return i
}
func (s *querySource) versionAt(i int) storage.RowVersion {
	if i == absentQueryIndex {
		return storage.RowVersion{}
	}
	if s.inheritance != nil {
		segment, index := s.inheritanceSegment(i)
		return segment.prefix.versionAt(segment.prefix.rawIndex(index))
	}
	if i < len(s.committed) {
		return s.committed[i]
	}
	return s.own[i-len(s.committed)]
}
func (s *querySource) close() {
	s.pgSequences = nil
	sources := s.sources
	s.sources = nil
	s.hashIndexes = nil
	if s.nestedStage != nil {
		s.nestedStage.Close()
		s.nestedStage = nil
	}
	s.nestedRoot = nil
	if s.nestedInputs != nil {
		s.nestedInputs.close()
		s.nestedInputs = nil
	}
	for _, child := range sources {
		if child != nil && child != s {
			child.close()
		}
	}
	for _, segment := range s.inheritance {
		segment.prefix.close()
	}
	s.inheritance, s.inheritanceEnds = nil, nil
	s.flat = nil
	s.windowValues, s.windowIndices = nil, nil
	s.presenceSlots, s.presenceConstant, s.presenceFlat = nil, nil, nil
	s.presenceCount = 0
	s.windowCount, s.windowWidth, s.window = 0, 0, false
	s.occurrences = nil
	s.provenance = nil
	s.versions = nil
	s.versionValid = nil
	s.joinLeft, s.joinRight, s.joinPairs = nil, nil, nil
	s.invocationLineage = nil
	s.flatCount = 0
	s.flatWidth = 0
	s.materialized = false
	lineage := s.lineage
	s.lineage = nil
	stageClosed := lineage != nil && s.final != nil && s.final.source == lineage.stage
	if s.final != nil {
		s.final.Close()
		s.final = nil
	}
	if lineage != nil {
		if !stageClosed {
			lineage.stage.close()
		}
		if lineage.inputs != lineage.stage {
			lineage.inputs.close()
		}
	}
	s.locks = nil
	if s.input != nil {
		s.input.close()
		s.input = nil
	}
	s.scratch, s.layout = nil, nil
	if s.view != nil {
		s.view.Release()
	}
	s.view = nil
	s.committed, s.own = nil, nil
	s.authority, s.host = nil, nil
	s.partialMask = nil
	s.anonymous = nil
	s.visible = nil
	s.routineRecipe, s.routineChecks = nil, nil
}

// Indexed stages retain occurrences, not copied input tuples. All scalar work
// delegates to the sole expression evaluator; each stage owns its private state.
type queryState struct {
	source       *querySource
	invocation   *EvalContext // Borrowed same-query P+L frame; outer remains lexical.
	foreignOwner any
	hashFilter   *plan.FilterNode
	parameters   []sqltype.Value
	input        sqltype.InputEnvironment
	output       sqltype.OutputEnvironment
	locking      bool
	virtualReads bool
	inherited    plan.RowMark
	frame        uint32
	outer        *EvalContext
	retain       bool
	derived      map[*plan.DerivedNode]*querySource
	ctes         *cteInvocation
	hashOwner    *queryState
	memberships  map[subqueryMembershipKey]*subqueryMembership
	// Only the positive SELECT coordinator activates these on a physical leaf.
	executionContext   context.Context
	scanRange          bool
	scanStart, scanEnd int
}

// Physical child stages borrow the actual execution owner. Rescanning a parent
// does not change an uncorrelated child's inputs or its membership lifetime.
func (s *queryState) membershipOwner() *queryState {
	if s.hashOwner == nil {
		s.hashOwner = s
	}
	return s.hashOwner
}

func (s *queryState) releaseMemberships() {
	if s.hashOwner != nil && s.hashOwner != s {
		return
	}
	for _, membership := range s.memberships {
		*membership = subqueryMembership{}
	}
	s.memberships = nil
	s.hashOwner = nil
}

type indexedStage struct {
	source             *querySource
	selection          []int
	hashCandidates     *hashCandidateSelection
	permutation        []int
	orderValues        []sqltype.Value
	cacheOccurrences   []int
	cacheWidth         int
	pending            []queryPendingMark
	schema             []catalog.Column
	expressions        []expr.Expr
	evaluation         EvalContext
	position           int
	terminal           error
	closed             bool
	produce            func(context.Context, *indexedStage) (bool, error)
	finish             func() error
	onFailure          func(error) error // Shared producers retain failures even before a reader pull.
	done               bool
	retain             bool
	reset              func(context.Context, *indexedStage) (bool, error)
	scanRange          bool
	scanStart, scanEnd int
}

func (s indexedStage) count() int {
	if s.selection != nil {
		return len(s.selection)
	}
	if s.scanRange {
		return s.scanEnd - s.scanStart
	}
	return s.source.count()
}
func (s indexedStage) index(i int) int {
	if s.permutation != nil {
		i = s.permutation[i]
	}
	if s.selection != nil {
		return s.selection[i]
	}
	if s.scanRange {
		return s.scanStart + i
	}
	return i
}
func (s *indexedStage) Schema() []catalog.Column { return s.schema }
func (s *indexedStage) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if s.terminal != nil {
		return false, s.terminal
	}
	if s.closed {
		return false, nil
	}
	if len(dst) != len(s.schema) {
		return false, s.fail(fmt.Errorf("stuntgres: invalid stage destination width"))
	}
	return s.nextProjected(ctx, dst)
}

// A nil destination discards values, not projection demand. Retained EXISTS
// shares this evaluator/validation seam without allocating an output tuple.
func (s *indexedStage) nextProjected(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if s.terminal != nil {
		return false, s.terminal
	}
	if s.closed {
		return false, nil
	}
	fail := func(err error) (bool, error) { return false, s.fail(err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	present, err := s.projectOrdinal(ctx, s.position, dst)
	if err != nil {
		return fail(err)
	}
	if !present && !s.retain {
		if err := s.Close(); err != nil {
			return fail(err)
		}
	}
	if present {
		s.position++
	}
	return present, nil
}

func (s *indexedStage) rewind(ctx context.Context) (bool, error) {
	s.position = 0
	if s.reset != nil {
		changed, err := s.reset(ctx, s)
		if err != nil {
			return false, s.fail(err)
		}
		return changed, nil
	}
	return false, nil
}

func (s *indexedStage) projectOrdinal(ctx context.Context, ordinal int, dst []sqltype.Value) (bool, error) {
	present, err := s.ensure(ctx, ordinal)
	if err != nil || !present {
		return present, err
	}
	row := s.source.row(s.index(ordinal))
	if s.expressions == nil {
		if dst != nil {
			copy(dst, row)
		}
	} else {
		s.evaluation.setContext(ctx)
		s.evaluation.Row = row
		s.evaluation.bindQueryIdentity(s.source, s.index(ordinal))
		if s.cacheWidth != 0 {
			s.evaluation.orderValues = s.orderRow(ordinal)
		}
		for i, expr := range s.expressions {
			value, err := expr.Eval(&s.evaluation)
			if err != nil {
				return false, s.fail(err)
			}
			if !value.ValidResult(s.schema[i].Usage) {
				return false, s.fail(fmt.Errorf("stuntgres: invalid finalized query value"))
			}
			if dst != nil {
				dst[i] = value
			}
		}
	}
	return true, nil
}
func (s *indexedStage) release() {
	// The query execution owner, not this borrowed stage, owns the view pin.
	s.source = nil
	s.selection = nil
	s.hashCandidates = nil
	s.expressions = nil
	s.permutation = nil
	s.orderValues = nil
	s.cacheOccurrences = nil
	s.pending = nil
	s.evaluation = EvalContext{}
}
func (s *indexedStage) Close() error {
	if s.closed {
		s.terminal = nil
		return nil
	}
	var err error
	if s.finish != nil {
		finish := s.finish
		s.finish = nil
		err = finish()
	}
	s.produce = nil
	s.reset = nil
	s.onFailure = nil
	s.release()
	s.closed = true
	s.terminal = nil
	return err
}

func (s *indexedStage) fail(err error) error {
	if s.onFailure != nil {
		err = s.onFailure(err)
	}
	_ = s.Close()
	s.terminal = err
	return err
}
func openQueryNode(ctx context.Context, state *queryState, node plan.Node) (indexedStage, error) {
	state.inheritCTEs()
	return openRetainedQueryNode(ctx, state, node)
}

func openRetainedQueryNode(ctx context.Context, state *queryState, node plan.Node) (out indexedStage, err error) {
	defer func() { out.retain = state.retain }()
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		return openFunctionFromNode(ctx, state, n)
	case *plan.SetProjectNode:
		return openSetProjectNode(ctx, state, n)
	case *plan.WindowNode:
		return openWindowNode(ctx, state, n)
	case *plan.SetNode:
		return openSetNode(ctx, state, n)
	case *plan.RecursiveUnionNode:
		return openRecursiveUnion(ctx, state, n)
	case *plan.WorktableScanNode:
		return openWorktableScan(ctx, state, n)
	case *plan.LimitNode:
		return openLimitNode(ctx, state, n)
	case *plan.EvaluationNode:
		return openEvaluationNode(ctx, state, n)
	case *plan.RowLockNode:
		return openRowLockNode(ctx, state, n)
	case *plan.JoinNode:
		return openJoinNode(ctx, state, n)
	case *plan.DistinctNode:
		return openDistinctNode(ctx, state, n)
	case *plan.DistinctOnNode:
		return openDistinctOnNode(ctx, state, n)
	case *plan.DerivedNode:
		return openDerivedNode(ctx, state, n)
	case *plan.AggregateNode:
		return openAggregateNode(ctx, state, n)
	case *plan.ValuesNode:
		if state.source == nil {
			return indexedStage{}, fmt.Errorf("stuntgres: invalid VALUES query source")
		}
		source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority}
		establishSourcePresence(source, n.PresenceSlot)
		out := indexedStage{source: source, schema: n.Schema(), selection: []int{}}
		evaluation := state.evaluation(nil)
		out.reset = func(ctx context.Context, out *indexedStage) (bool, error) {
			source.flat, source.flatCount = source.flat[:0], 0
			out.selection, out.done = out.selection[:0], false
			return true, ctx.Err()
		}
		out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
			i := source.flatCount
			if i == len(n.Rows) {
				return false, nil
			}
			evaluation.setContext(ctx)
			for j, expr := range n.Rows[i] {
				value, err := expr.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				if err := validateProducedValue(value, n.Schema()[j].Usage); err != nil {
					return false, err
				}
				source.flat = append(source.flat, value)
			}
			out.selection = append(out.selection, i)
			source.flatCount++
			return true, ctx.Err()
		}
		return out, nil
	case *plan.SingletonNode:
		if state.source == nil {
			return indexedStage{}, fmt.Errorf("stuntgres: missing singleton statement source")
		}
		source := &querySource{singleton: true, authority: state.source.authority, host: state.source.host, locks: state.source.locks}
		return indexedStage{source: source, schema: n.Schema()}, nil
	case *plan.SourceNode:
		source := state.source
		if source != nil && source.sources != nil {
			if n.Occurrence < 0 || n.Occurrence >= len(source.sources) {
				return indexedStage{}, fmt.Errorf("stuntgres: invalid query occurrence")
			}
			source = source.sources[n.Occurrence]
		}
		if source == nil || source.ref != n.Ref && (source.storedRef == (ids.ObjectRef{}) || source.storedRef != n.Ref) {
			return indexedStage{}, fmt.Errorf("stuntgres: invalid query source target")
		}
		establishSourcePresence(source, n.PresenceSlot)
		if source.foreign != nil {
			return openForeignStage(ctx, state, source, n)
		}
		if source.pgSequences != nil {
			return openPGSequencesStage(state, source, n.Schema()), nil
		}
		if source.final != nil || source.input != nil || source.nestedRoot != nil {
			return openSourceStage(ctx, state, source, n.Schema())
		}
		if source.anonymous == nil {
			if source.view == nil {
				return indexedStage{}, fmt.Errorf("stuntgres: missing query source")
			}
			data := source.view.Data()
			if source.ref.Domain() != data.Catalog.Owner().Domain || source.ref.Epoch != data.Catalog.Epoch() {
				return indexedStage{}, fmt.Errorf("stuntgres: invalid query source target")
			}
		}
		return indexedStage{source: source, schema: n.Schema(), scanRange: state.scanRange, scanStart: state.scanStart, scanEnd: state.scanEnd}, nil
	case *plan.FilterNode:
		evaluation := state.evaluation(ctx)
		rejects, err := queryPredicateRejectsAll(&evaluation, n.Predicate)
		if err != nil {
			return indexedStage{}, err
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
		if rejects && !finalizesOrderedSet && !queryHasMandatoryPhysicalFrontier(n.Child, state.source) {
			// Only child-reachable consumed physical marks require the eager
			// input frontier. Unrelated semantic context, identity-only manifests
			// and unopened opaque CTE bodies do not demand this child.
			source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, host: state.source.host, locks: state.source.locks}
			return indexedStage{source: source, schema: n.Schema()}, nil
		}
		if join, ok := n.Child.(*plan.JoinNode); ok && !queryHasMandatoryPhysicalFrontier(n.Child, state.source) {
			if partitioned := functionFromConjuncts(join, n.Predicate); partitioned != nil {
				return openQueryNode(ctx, state, partitioned)
			}
			if join := functionFromWhere(n.Child, n.Predicate); join != nil {
				// Keep canonical/view literal paths intact. Only this execution
				// filters predecessor rows before opening their correlated RHS.
				// EPQ instead retains its captured, identity-keyed topology even
				// after its physical locking frontier has been cleared.
				return openQueryNode(ctx, state, join)
			}
		}
		admitPGSequencesPredicate(n.Child, state.source, n.Predicate, 0, evaluation.Frame)
		child, err := openQueryNode(ctx, state, n.Child)
		if err != nil {
			return indexedStage{}, err
		}
		candidates, err := hashFilterCandidates(ctx, state, n, child.source)
		if err != nil {
			child.Close()
			return indexedStage{}, err
		}
		out := producingStage(&child, n.Schema())
		out.hashCandidates = candidates
		baseReset := out.reset
		out.reset = func(ctx context.Context, out *indexedStage) (bool, error) {
			changed, err := baseReset(ctx, out)
			if err != nil {
				return false, err
			}
			if n.Predicate.Properties().Volatility == expr.Volatile {
				if !changed {
					if _, err := child.rewind(ctx); err != nil {
						return false, err
					}
				}
				out.selection, out.cacheOccurrences, out.done = out.selection[:0], out.cacheOccurrences[:0], false
				if out.hashCandidates != nil {
					out.hashCandidates.position = 0
				}
				return true, nil
			}
			return changed, nil
		}
		out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
			for {
				var i int
				if candidates := out.hashCandidates; candidates != nil {
					if candidates.position == len(candidates.occurrences) {
						return false, ctx.Err()
					}
					i = candidates.occurrences[candidates.position]
					candidates.position++
				} else {
					var present bool
					var err error
					i, present, err = child.nextOccurrence(ctx)
					if err != nil || !present {
						return present, err
					}
				}
				if mask := child.source.partialMask; mask != nil {
					row := child.source.versionAt(child.source.rawIndex(child.index(i)))
					if !partialMaskContains(mask, row) {
						continue
					}
				}
				evaluation.setContext(ctx)
				evaluation.bindQueryRow(child.source, child.index(i))
				value, err := n.Predicate.Eval(&evaluation)
				if err != nil {
					return false, err
				}
				truth, err := plan.TruthOf(value)
				if err != nil {
					return false, err
				}
				if truth == plan.True {
					retainOccurrence(out, &child, i)
					return true, ctx.Err()
				}
			}
		}
		return out, nil
	case *plan.SortNode:
		if n.PresenceOnly {
			return openQueryNode(ctx, state, n.Child)
		}
		child, err := openQueryNode(ctx, state, n.Child)
		if err != nil {
			return indexedStage{}, err
		}
		child, err = settlePendingQueryMarks(ctx, state, n.Child, child)
		if err != nil {
			return indexedStage{}, err
		}
		transferred := false
		defer func() {
			if !transferred {
				child.Close()
			}
		}()
		if err := child.drain(ctx); err != nil {
			return indexedStage{}, err
		}
		if child.selection == nil {
			child.selection = make([]int, child.count())
			for i := range child.selection {
				child.selection[i] = i
				if child.scanRange {
					child.selection[i] += child.scanStart
				}
			}
		}
		if n.CacheWidth != 0 {
			child.cacheWidth = n.CacheWidth
			child.cacheOccurrences = nil
			child.orderValues = make([]sqltype.Value, child.count()*n.CacheWidth)
			child.permutation = make([]int, child.count())
			evaluation := state.evaluation(ctx)
			for i := range child.count() {
				child.permutation[i] = i
				evaluation.bindQueryRow(child.source, child.selection[i])
				for _, key := range n.Keys {
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
					if key.Cache < 0 || child.orderValues[i*n.CacheWidth+key.Cache].Type() != nil {
						continue
					}
					value, err := key.Expression.Eval(&evaluation)
					if err != nil {
						return indexedStage{}, err
					}
					if value.Type() != key.Expression.ResultUsage().Type {
						return indexedStage{}, fmt.Errorf("stuntgres: invalid ordering value")
					}
					child.orderValues[i*n.CacheWidth+key.Cache] = value
					if err := ctx.Err(); err != nil {
						return indexedStage{}, err
					}
				}
			}
			for i := range child.count() {
				evaluation.bindQueryRow(child.source, child.selection[i])
				for j, expr := range n.Outputs {
					value, err := expr.Eval(&evaluation)
					if err != nil {
						return indexedStage{}, err
					}
					child.orderValues[i*n.CacheWidth+n.CacheWidth-len(n.Outputs)+j] = value
				}
			}
		}
		indices := child.selection
		if child.permutation != nil {
			indices = child.permutation
		}
		err = mergeQueryIndices(ctx, indices, func(a, b int) (int, error) {
			leftIndex, rightIndex := a, b
			var leftValues, rightValues []sqltype.Value
			if child.permutation != nil {
				leftIndex, rightIndex = child.selection[a], child.selection[b]
				leftValues = child.orderValues[a*n.CacheWidth : (a+1)*n.CacheWidth]
				rightValues = child.orderValues[b*n.CacheWidth : (b+1)*n.CacheWidth]
			}
			return compareQueryOrder(ctx, n.Keys, state.input,
				querySortRow{source: child.source, index: leftIndex, values: leftValues},
				querySortRow{source: child.source, index: rightIndex, values: rightValues})
		})
		transferred = err == nil
		child.reset = nil
		return child, err
	case *plan.ProjectNode:
		if n.RuleInvocation {
			local := *state
			local.outer = state.invocation
			state = &local
		}
		if !state.virtualReads {
			state.virtualReads = queryHasVirtualReads(n)
		}
		var owner *cteInvocation
		if n.CTEOwner {
			state.membershipOwner()
			owner = &cteInvocation{frame: n.Frame, parent: state.ctes, state: *state}
			state.ctes = owner
		}
		if n.Frame != 0 {
			state.frame = n.Frame
		}
		child, err := openQueryNode(ctx, state, n.Child)
		if err != nil {
			if owner != nil {
				_ = owner.close()
			}
			return indexedStage{}, err
		}
		child.schema = n.Schema()
		child.expressions = n.Expressions
		child.evaluation = state.evaluation(nil)
		if owner != nil {
			finish := child.finish
			child.finish = func() error {
				var first error
				if finish != nil {
					first = finish()
				}
				if failure := owner.close(); first == nil {
					first = failure
				}
				return first
			}
		}
		return child, nil
	default:
		return indexedStage{}, fmt.Errorf("stuntgres: invalid logical query node")
	}
}

// Unlike a bool sort callback, every comparison can stop production with its
// original error. One scratch buffer serves every merge level.
func mergeQueryIndices(ctx context.Context, indices []int, compare func(int, int) (int, error)) error {
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

// finalRows contains only finalized output, never expressions or parameters.
// self prevents forged or copied handles from releasing another cursor's pins.
type finalRows struct {
	self               *finalRows
	schema             []catalog.Column
	source             *querySource
	owner              *querySource // Capture tree when projection retains a non-root occurrence.
	selection, slots   []int
	values             []sqltype.Value
	regclassOutput     map[string]string
	regtypeOutput      map[uint32]string
	regprocOutput      map[uint32]string
	regprocedureOutput map[uint32]string
	regroleOutput      map[uint32]string
	operatorOutput     map[operatorOutputKey]string
	operatorGroups     operatorGroups
	regobjectOutput    map[ids.ObjectAddress]string
	regTSOutput        map[regTSOutputKey]string
	aclAuthority       *sqltype.ACLAuthority
	aclitemOutput      map[uint32]string
	anonymous          [][]sqltype.Value
	count, position    int
	terminal           error
	exhausted, closed  bool
	output             sqltype.OutputEnvironment
}

func newFinalRows(schema []catalog.Column, count int) *finalRows {
	r := &finalRows{schema: schema, count: count}
	r.self = r
	return r
}
func (r *finalRows) Schema() []catalog.Column                     { return r.schema }
func (r *finalRows) OutputEnvironment() sqltype.OutputEnvironment { return r.output }
func (r *finalRows) RegclassText(v sqltype.Value) (string, error) {
	key, err := v.Text()
	if err != nil {
		return "", err
	}
	text, ok := r.regclassOutput[key]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
	}
	return text, nil
}
func (r *finalRows) FormatRegclass(id uint32) (string, error) {
	text, ok := r.regclassOutput[strconv.FormatUint(uint64(id), 10)]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
	}
	return text, nil
}
func (r *finalRows) release() {
	if r.owner != nil {
		r.owner.close()
		r.owner = nil
	} else if r.source != nil {
		r.source.close()
	}
	r.source = nil
	r.selection, r.slots, r.values = nil, nil, nil
	r.anonymous = nil
	r.regclassOutput = nil
	r.regtypeOutput = nil
	r.regprocOutput = nil
	r.regroleOutput = nil
	r.operatorOutput = nil
	r.operatorGroups = nil
	r.regprocedureOutput = nil
	r.regobjectOutput = nil
	r.regTSOutput = nil
	r.aclitemOutput = nil
	r.aclAuthority = nil
}
func (r *finalRows) fail(err error) (bool, error) {
	r.terminal = err
	r.release()
	return false, err
}
func (r *finalRows) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if r == nil || r.self != r {
		return false, fmt.Errorf("stuntgres: invalid result owner")
	}
	if r.closed || r.exhausted {
		return false, nil
	}
	if r.terminal != nil {
		return false, r.terminal
	}
	if err := ctx.Err(); err != nil {
		return r.fail(err)
	}
	if len(dst) != len(r.schema) {
		return r.fail(fmt.Errorf("stuntgres: invalid result destination width"))
	}
	if r.position == r.count {
		r.exhausted = true
		r.release()
		return false, nil
	}
	if r.source != nil {
		index := r.position
		if r.selection != nil {
			index = r.selection[index]
		}
		row := r.source.row(index)
		for i, slot := range r.slots {
			dst[i] = row[slot]
		}
	} else {
		if r.anonymous != nil {
			copy(dst, r.anonymous[r.position])
		} else {
			copy(dst, r.values[r.position*len(dst):(r.position+1)*len(dst)])
		}
	}
	r.position++
	return true, nil
}
func (r *finalRows) Close() error {
	if r != nil && r.self == r && !r.closed {
		r.release()
		r.terminal = nil
		r.closed = true
	}
	return nil
}

// A command sink takes already-finalized values, never evaluating expressions
// again. Owned flat values transfer directly; a borrowed projection is copied
// once to sever its source lifetime and slot mapping before catalog publication.
func (r *finalRows) takeTableRows(ctx context.Context) (rows [][]sqltype.Value, err error) {
	if r == nil || r.self != r || r.closed || r.position != 0 {
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
	if r.anonymous != nil {
		rows = r.anonymous
		r.anonymous = nil
		return rows, nil
	}
	rows = make([][]sqltype.Value, r.count)
	width := len(r.schema)
	values := r.values
	if r.source != nil {
		values = make([]sqltype.Value, r.count*width)
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
		r.values = nil
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
func openQueryProjectStage(ctx context.Context, state *queryState, project *plan.ProjectNode) (indexedStage, error) {
	child, err := openQueryNode(ctx, state, project)
	if err != nil {
		return indexedStage{}, err
	}
	settled, err := settlePendingQueryMarks(ctx, state, project.Child, child)
	if err != nil {
		_ = child.Close()
		return indexedStage{}, err
	}
	return settled, nil
}

func finalizeQuery(ctx context.Context, root plan.Node, source *querySource, parameters []sqltype.Value) (RowResult, int, error) {
	keep := false
	defer func() {
		_ = finishForeignSources(ctx, source)
		if !keep {
			source.close()
		}
	}()
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return nil, 0, fmt.Errorf("stuntgres: query lacks final projection")
	}
	state := &queryState{source: source, parameters: parameters, locking: source.locks != nil, hashFilter: hashQueryFilter(root)}
	defer state.releaseMemberships()
	if host := source.liveHost(); host != nil {
		state.input, state.output = host.Environments()
	}
	stage, err := openQueryProjectStage(ctx, state, project)
	if err != nil {
		return nil, 0, err
	}
	defer stage.Close()
	if err := stage.drain(ctx); err != nil {
		return nil, 0, err
	}
	r := newFinalRows(project.Schema(), stage.count())
	r.output = state.output
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
		r.values = make([]sqltype.Value, r.count*width)
	}
	if direct {
		slots = make([]int, len(project.Expressions))
		for i, expr := range project.Expressions {
			slots[i] = expr.(plan.ColumnExpr).Slot
		}
		evaluation := state.evaluation(ctx)
		for i := range r.count {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			evaluation.bindQueryRow(stage.source, stage.index(i))
			for j, expr := range project.Expressions {
				value, err := expr.Eval(&evaluation)
				if err != nil {
					return nil, 0, err
				}
				if !value.ValidResult(r.schema[j].Usage) {
					return nil, 0, fmt.Errorf("stuntgres: invalid finalized query value")
				}
			}
		}
	} else {
		for i := range r.count {
			present, err := stage.Next(ctx, r.values[i*width:(i+1)*width])
			if err != nil {
				return nil, 0, err
			}
			if !present {
				return nil, 0, fmt.Errorf("stuntgres: early projection EOF")
			}
		}
	}
	selection := stage.selection
	if stage.permutation != nil && direct {
		// Final physical output identity is distinct from sort-cache identity.
		selection = stage.permutation
		if stage.selection != nil {
			for i, occurrence := range selection {
				selection[i] = stage.selection[occurrence]
			}
		}
	}
	stageSource := stage.source
	if err := stage.Close(); err != nil {
		return nil, 0, err
	}
	if err := finishForeignSources(ctx, source); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if direct && r.count > 0 {
		r.source, r.selection, r.slots = stageSource, selection, slots
		keep = true
		if stageSource != source && !querySourceRetains(stageSource, source) {
			if querySourceRetains(source, stageSource) {
				r.owner = source
				detachQuerySource(source)
			} else {
				source.close()
			}
		}
	}
	if !direct || r.count == 0 {
		stageSource.close()
	}
	detachQuerySource(stageSource)
	return r, r.count, nil
}

// Anonymous producers transfer their already-owned typed rows into the same
// final cursor contract; empty relations still carry an explicit schema.
func anonymousResult(schema []catalog.Column, rows [][]sqltype.Value, count int64) (Result, error) {
	r := newFinalRows(schema, len(rows))
	r.anonymous = rows
	for _, row := range rows {
		if len(row) != len(schema) {
			return Result{}, fmt.Errorf("stuntgres: invalid anonymous result width")
		}
		for j, value := range row {
			if !value.ValidResult(schema[j].Usage) {
				return Result{}, fmt.Errorf("stuntgres: invalid anonymous result type")
			}
		}
	}
	return Result{Count: count, Rows: r}, nil
}

func querySourceRetains(source, target *querySource) bool {
	if source == target {
		return true
	}
	if source == nil {
		return false
	}
	if querySourceRetains(source.input, target) {
		return true
	}
	for _, child := range source.sources {
		if child != source && querySourceRetains(child, target) {
			return true
		}
	}
	return false
}
