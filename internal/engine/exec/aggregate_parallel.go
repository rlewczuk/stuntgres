package exec

import (
	"context"
	"fmt"
	"sync"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type parallelOperationKey struct{}

// ParallelOperationContext covers both leader and worker execution. Worker
// identity is separate: leader combine/final support has the same effect fence.
func ParallelOperationContext(ctx context.Context) context.Context {
	if InParallelOperation(ctx) {
		return ctx
	}
	return context.WithValue(ctx, parallelOperationKey{}, true)
}

func InParallelOperation(ctx context.Context) bool {
	return ctx != nil && ctx.Value(parallelOperationKey{}) == true
}

type parallelWorkerKey struct{}

func ParallelWorkerContext(ctx context.Context) context.Context {
	if InParallelWorker(ctx) {
		return ctx
	}
	return context.WithValue(ParallelOperationContext(ctx), parallelWorkerKey{}, true)
}

func InParallelWorker(ctx context.Context) bool {
	return ctx != nil && ctx.Value(parallelWorkerKey{}) == true
}

// The worker retains immutable admission evidence, not the SQL host's lexical
// scratch, which a nested support body may replace under its invocation gate.
type parallelAggregateAdmission struct {
	access       RoutineAccess
	dependencies []catalog.ExpressionDependency
}

func (a *parallelAggregateAdmission) AdmissionAccess() RoutineAccess { return a.access }

func ParallelAggregatePlan(root plan.Node, source *Source) (*plan.AggregateNode, bool) {
	project, ok := root.(*plan.ProjectNode)
	if !ok || project.RuleInvocation {
		return nil, false
	}
	frame := project.Frame
	if frame == 0 {
		frame = 1
	}
	node := project.Child
	outer := func(e expr.Expr) bool { return parallelQueryExpression(e, frame, len(project.Child.Schema()), true) }
	for _, e := range project.Expressions {
		if !outer(e) {
			return nil, false
		}
	}
	for _, e := range project.AnalysisExpressions {
		if !outer(e) {
			return nil, false
		}
	}
	if ordered, ok := node.(*plan.SortNode); ok {
		if ordered.PresenceOnly {
			return nil, false
		}
		for _, key := range ordered.Keys {
			if !outer(key.Expression) {
				return nil, false
			}
		}
		for _, e := range ordered.Outputs {
			if !outer(e) {
				return nil, false
			}
		}
		node = ordered.Child
	}
	if having, ok := node.(*plan.FilterNode); ok {
		if !outer(having.Predicate) {
			return nil, false
		}
		node = having.Child
	}
	a, ok := node.(*plan.AggregateNode)
	if !ok || a.PresenceOnly || a.GroupingActive || len(a.Calls) == 0 {
		return nil, false
	}
	if a.Sets != nil {
		if len(a.Sets) != 1 || len(a.Sets[0]) != len(a.Keys) {
			return nil, false
		}
		for i, key := range a.Sets[0] {
			if i != key {
				return nil, false
			}
		}
	}
	// Reuse all incumbent physical-source and input-filter gates, without
	// interpreting aggregate output slots as physical input slots.
	shadow := *project
	shadow.Child, shadow.Expressions, shadow.AnalysisExpressions = a.Child, nil, nil
	if _, _, eligible := ParallelQueryPlan(&shadow, source); !eligible {
		return nil, false
	}
	input := func(e expr.Expr) bool { return ParallelQueryExpression(e, frame, len(a.Child.Schema())) }
	for _, key := range a.Keys {
		if !input(key) {
			return nil, false
		}
	}
	for _, call := range a.Calls {
		partial, ok := call.Provider.(functions.PartialAggregateProvider)
		if !ok || !partial.PartialCapable() || len(call.Order) != 0 || len(call.Distinct) != 0 || len(call.Direct) != 0 {
			return nil, false
		}
		if p := call.Procedure; p != nil {
			if p.Aggregate != nil {
				if p.Aggregate.Parallel != 's' {
					return nil, false
				}
			} else if p.Builtin == nil || p.Builtin.Parallel == 'u' || p.Builtin.Parallel == 'r' {
				return nil, false
			}
		}
		if !input(call.Filter) {
			return nil, false
		}
		for _, argument := range call.Arguments {
			if !input(argument) {
				return nil, false
			}
		}
	}
	return a, true
}

func FinalizeParallelAggregate(ctx context.Context, root plan.Node, aggregate *plan.AggregateNode, source *Source, parameters []sqltype.Value, workers int) (RowResult, int, error) {
	defer source.Close()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	ctx = ParallelOperationContext(ctx)
	input, output := sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}
	if host := source.LiveHost(); host != nil {
		input, output = host.Environments()
	}
	leader := &State{Source: source, Parameters: parameters, Input: input, Output: output, ExecutionContext: ctx}
	defer leader.ReleaseMemberships()
	evaluation := leader.Evaluation(ctx)
	auth, dependencies, err := RoutineAdmissionAuthority(&evaluation)
	if err != nil {
		return nil, 0, err
	}
	var admission *parallelAggregateAdmission
	if auth.Live() {
		admission = &parallelAggregateAdmission{access: auth, dependencies: dependencies}
	}
	for i := range aggregate.Calls {
		call := aggregate.Calls[i]
		if err := AdmitAggregatePartialSupport(&evaluation, &call, false); err != nil {
			return nil, 0, err
		}
		if err := AdmitAggregatePartialSupport(&evaluation, &call, true); err != nil {
			return nil, 0, err
		}
		if p, ok := call.Provider.(*TransitionAggregate); ok {
			if err := p.Start(&evaluation); err != nil {
				return nil, 0, err
			}
		}
	}
	workerCtx, cancel := context.WithCancel(ParallelWorkerContext(ctx))
	defer cancel()
	runs := make([][]QueryGroup, workers)
	var joined sync.WaitGroup
	var errorMu sync.Mutex
	var first error
	n := source.Count()
	inheritedCTEs := leader.Ctes // Capture even nil before any support body can mutate host scratch.
	for i := range workers {
		joined.Add(1)
		go func(i int) {
			defer joined.Done()
			groups, failure := runAggregatePartition(workerCtx, aggregate, source, parameters, i*n/workers, (i+1)*n/workers, input, output, admission, inheritedCTEs)
			if failure != nil {
				errorMu.Lock()
				if first == nil {
					first = failure
					cancel()
				}
				errorMu.Unlock()
				return
			}
			runs[i] = groups
		}(i)
	}
	joined.Wait()
	if first != nil {
		return nil, 0, first
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	groups, err := mergeAggregatePartitions(ctx, aggregate, runs, &evaluation)
	if err != nil {
		return nil, 0, err
	}
	rows := make([][]sqltype.Value, len(groups))
	for i, group := range groups {
		for j, call := range aggregate.Calls {
			var value sqltype.Value
			var err error
			if provider, ok := call.Provider.(functions.ExecutionAggregateProvider); ok {
				value, err = provider.FinalizeWithContext(ctx, group.states[j], nil, &evaluation)
			} else {
				value, err = call.Provider.Finalize(ctx, group.states[j])
			}
			if err != nil {
				return nil, 0, err
			}
			if value.Type() != call.Provider.ResultUsage().Type {
				return nil, 0, fmt.Errorf("stuntgres: foreign partial result")
			}
			group.Keys[len(aggregate.Keys)+j] = value
		}
		rows[i] = group.Keys
	}
	materialized := &Source{Anonymous: rows, Materialized: true, FlatCount: len(rows), Authority: source.Authority, Host: source.Host}
	result, count, err := Finalize(ctx, aggregateResultGraph(root, aggregate), materialized, parameters)
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		result.Close()
		return nil, 0, err
	}
	return result, count, nil
}

func runAggregatePartition(ctx context.Context, aggregate *plan.AggregateNode, source *Source, parameters []sqltype.Value, start, end int, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, admission *parallelAggregateAdmission, ctes *CTEInvocation) ([]QueryGroup, error) {
	worker, err := ParallelQuerySource(source)
	if err != nil {
		return nil, err
	}
	defer worker.Close()
	var groups []QueryGroup
	state := &State{Source: worker, Parameters: parameters, Input: input, Output: output, ExecutionContext: ctx, ScanRange: true, ScanStart: start, scanEnd: end, aggregatePartials: &groups, aggregateAdmission: admission, Ctes: ctes}
	defer state.ReleaseMemberships()
	evaluation := state.Evaluation(ctx)
	for _, call := range aggregate.Calls {
		if err := AdmitAggregatePartialSupport(&evaluation, &call, true); err != nil {
			return nil, err
		}
	}
	_, err = openAggregateNode(ctx, state, aggregate)
	if err != nil {
		return nil, err
	}
	// Cross the actual opaque codec boundary before releasing the worker pin.
	for i := range groups {
		for j, call := range aggregate.Calls {
			if codec, ok := call.Provider.(functions.AggregateStateTransport); ok {
				data, present, err := codec.Serialize(ctx, groups[i].states[j])
				if err != nil {
					return nil, err
				}
				groups[i].states[j] = aggregateTransport{data: data, present: present}
			}
		}
	}
	return groups, ctx.Err()
}

type aggregateTransport struct {
	data    []byte
	present bool
}

func mergeAggregatePartitions(ctx context.Context, n *plan.AggregateNode, runs [][]QueryGroup, evaluation *Context) ([]QueryGroup, error) {
	groups := make([]QueryGroup, 0)
	buckets := make(map[uint64][]int)
	active := make([]int, len(n.Keys))
	for i := range active {
		active[i] = i
	}
	for _, run := range runs {
		for _, incoming := range run {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			hash := uint64(14695981039346656037)
			for i, family := range n.Grouping {
				h, err := family.Hash(ctx, incoming.Keys[i])
				if err != nil {
					return nil, err
				}
				hash = (hash ^ h) * 1099511628211
			}
			index, err := FindQueryGroup(ctx, n.Grouping, incoming.Keys, active, groups, buckets[hash])
			if err != nil {
				return nil, err
			}
			if index < 0 {
				group := QueryGroup{Keys: append([]sqltype.Value(nil), incoming.Keys...), states: make([]any, len(n.Calls))}
				for j, call := range n.Calls {
					if provider, ok := call.Provider.(functions.ExecutionAggregateProvider); ok {
						group.states[j], err = provider.InitWithContext(ctx, evaluation)
					} else if provider, ok := call.Provider.(functions.ContextualAggregateProvider); ok {
						group.states[j], err = provider.InitWithEnvironment(ctx, evaluation.Output)
					} else {
						group.states[j], err = call.Provider.Init(ctx)
					}
					if err != nil {
						return nil, err
					}
				}
				index = len(groups)
				groups = append(groups, group)
				buckets[hash] = append(buckets[hash], index)
			}
			for j, call := range n.Calls {
				state := incoming.states[j]
				if transport, ok := state.(aggregateTransport); ok {
					state, err = call.Provider.(functions.AggregateStateTransport).Deserialize(ctx, transport.data, transport.present)
					if err != nil {
						return nil, err
					}
				}
				if err := call.Provider.(functions.PartialAggregateProvider).Combine(ctx, groups[index].states[j], state, evaluation); err != nil {
					return nil, err
				}
			}
		}
	}
	return groups, ctx.Err()
}

// Copy only the leader's small immutable wrapper path. Input and aggregate
// expressions remain original retained recipes; no per-row rebinding occurs.
func aggregateResultGraph(node plan.Node, aggregate *plan.AggregateNode) plan.Node {
	if node == aggregate {
		return &plan.SourceNode{QuerySchema: aggregate.QuerySchema}
	}
	switch n := node.(type) {
	case *plan.ProjectNode:
		next := *n
		next.Child = aggregateResultGraph(n.Child, aggregate)
		return &next
	case *plan.SortNode:
		next := *n
		next.Child = aggregateResultGraph(n.Child, aggregate)
		return &next
	case *plan.FilterNode:
		next := *n
		next.Child = aggregateResultGraph(n.Child, aggregate)
		return &next
	default:
		panic("stuntgres: unadmitted parallel aggregate wrapper")
	}
}
