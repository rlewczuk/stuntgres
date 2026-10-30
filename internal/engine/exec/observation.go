package exec

import (
	"context"
	"sync"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

// Observation belongs to one fully settled execution, never a retained plan.
// Each opened stage writes only its own record. The registry lock is used at
// stage creation and after joined completion, never in the row loop.
type Observation struct {
	mu sync.Mutex
	timing bool
	records map[plan.Node][]*stageObservation
	foreignOptions catalog.ForeignExplainOptions
	foreign map[plan.Node]ForeignObservation
}

type Actual struct {
	Rows, Loops int64
	Elapsed time.Duration
}

type observationKey struct{}

// Result wrappers copied by parallel aggregation retain their original logical
// identities only for this leader invocation, never on the retained graph.
type observationAliasesKey struct{}

// HasObservation lets cross-package selection owners skip observation-only
// defensive copies before constructing callback arguments on the ordinary path.
func HasObservation(ctx context.Context) bool {
	o, _ := ctx.Value(observationKey{}).(*Observation)
	return o != nil
}

func Observe(ctx context.Context, timing bool) (context.Context, *Observation) {
	o := &Observation{timing: timing, records: make(map[plan.Node][]*stageObservation)}
	return context.WithValue(ctx, observationKey{}, o), o
}

// Context carries this command identity without replacing the caller's runtime
// cancellation, pause admission or evaluation environment.
func (o *Observation) Context(ctx context.Context) context.Context {
	if current, _ := ctx.Value(observationKey{}).(*Observation); current == o {
		return ctx
	}
	return context.WithValue(ctx, observationKey{}, o)
}

// Register admits genuine prepared graph identities, including never-demanded
// descendants. Synthetic presentation-only DML scans are deliberately absent.
func (o *Observation) Register(root plan.Node) {
	if o == nil { return }
	o.mu.Lock()
	defer o.mu.Unlock()
	var visit func(plan.Node)
	visit = func(node plan.Node) {
		if node == nil { return }
		if _, exists := o.records[node]; exists { return }
		o.records[node] = nil
		left, right := plan.QueryInputs(node)
		visit(left)
		visit(right)
		plan.QueryExpressions(node, func(expression expr.Expr) {
			plan.WalkDemandedExpression(expression, func(expression expr.Expr) {
				if subquery, ok := expression.(plan.SubqueryExpr); ok && subquery.QueryPlan != nil {
					visit(subquery.QueryPlan.Root)
				}
			})
		})
	}
	visit(root)
}

func (o *Observation) Known(node plan.Node) bool {
	if o == nil || node == nil { return false }
	o.mu.Lock()
	_, known := o.records[node]
	o.mu.Unlock()
	return known
}

func (o *Observation) Actual(node plan.Node) Actual {
	var out Actual
	if o == nil { return out }
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, record := range o.records[node] {
		out.Rows += record.rows
		out.Loops += record.loops
		out.Elapsed += record.elapsed
	}
	return out
}

type stageObservation struct {
	timing bool
	entered bool
	frontier int
	rows, loops int64
	elapsed time.Duration
	depth int
	started time.Time
}

func (o *Observation) stage(node plan.Node) *stageObservation {
	r := &stageObservation{timing: o.timing}
	o.mu.Lock()
	o.records[node] = append(o.records[node], r)
	o.mu.Unlock()
	return r
}

// start/end delimit active work, excluding time between consumer demands.
// Reentrant projection/production calls share one inclusive span.
func (r *stageObservation) start() {
	if !r.entered { r.entered = true; r.loops++ }
	r.startWork()
}

// Partial aggregate work contributes elapsed time, not logical output or loops.
func (r *stageObservation) startWork() {
	if r.timing && r.depth == 0 { r.started = time.Now() }
	r.depth++
}

func (r *stageObservation) end() {
	r.depth--
	if r.timing && r.depth == 0 { r.elapsed += time.Since(r.started) }
}

func (r *stageObservation) emitted(ordinal int) {
	if ordinal >= r.frontier {
		r.rows++
		r.frontier = ordinal + 1
	}
}

func (s *Stage) observeStart() {
	for _, r := range s.observations { r.start() }
}
func (s *Stage) observeEnd() {
	for _, r := range s.observations { r.end() }
}
func (s *Stage) observeOutput(ordinal int) {
	if s.projection != nil { s.projection.emitted(ordinal) }
	for _, r := range s.observations { r.emitted(ordinal) }
}

// Split projection consumers own evaluation after NextOccurrence returns.
// Nesting with that pull shares one interval without inventing another output.
func (s *Stage) beginProjectionWork() {
	if s.projection != nil { s.projection.startWork() }
}
func (s *Stage) endProjectionWork() {
	if s.projection != nil { s.projection.end() }
}

func (s *Stage) observeDrained(count int) {
	if record := s.projection; record != nil && count > record.frontier {
		record.rows += int64(count - record.frontier)
		record.frontier = count
	}
	for _, r := range s.observations {
		if count > r.frontier {
			r.rows += int64(count - r.frontier)
			r.frontier = count
		}
	}
}

// Candidate-probing consumers bypass the sequential cursor. Each selected
// occurrence crosses the same source output boundary, without counting skipped
// source ordinals or inferring rows from the backing source's capacity.
func (s *Stage) observeIndexedRead() {
	for _, record := range s.observations {
		record.start()
		record.rows++
		record.frontier++
		record.end()
	}
}

func (s *Stage) observedEnsure(ctx context.Context, ordinal int) (bool, error) {
	if len(s.observations) != 0 {
		s.observeStart()
		defer s.observeEnd()
	}
	return s.ensure(ctx, ordinal)
}

// These openers execute blocking kernels before returning a stage. Opening a
// lazy scan is not demand, but entering a real blocking kernel is execution even
// when its materialized output is subsequently unused.
func eagerObservationNode(node plan.Node) bool {
	switch node := node.(type) {
	case *plan.AggregateNode:
		return !node.PresenceOnly
	case *plan.WindowNode:
		return true
	case *plan.SortNode:
		return !node.PresenceOnly
	case *plan.DistinctNode:
		return !node.PresenceOnly
	}
	return false
}
