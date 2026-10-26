package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAggregateCombineStateSemantics(t *testing.T) {
	d := transitionUnitType(t)
	u := d.Usage()
	conversion, err := sqltype.PlanCoercion(u, u, sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(d, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ctx := context.Background()
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	owner := &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}
	cp := &TransitionAggregate{Aggregate: owner, Support: &catalog.Procedure{Strict: true}, StateUsage: u, Result: u, Inputs: []sqltype.Usage{u, u}, Formals: []sqltype.Usage{u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion}, SeedConversion: conversion}
	p := *cp
	p.CombinePlan = cp
	calls := 0
	cp.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(c expr.Context) (sqltype.Value, error) {
		calls++
		args := c.(*Context).Parameters
		left, _ := args[0].Integer()
		right, _ := args[1].Integer()
		if left != 5 || right != 25 {
			t.Fatal("combine replayed original inputs", left, right)
		}
		return sqltype.Null(d), nil
	}}
	leader, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	incoming := func(v sqltype.Value) any { return &transitionAggregateState{owner: &p, value: v} }
	for _, state := range []any{incoming(sqltype.Null(d)), incoming(value(5)), incoming(value(25)), incoming(value(99))} {
		if err := p.Combine(ctx, leader, state, evaluation); err != nil {
			t.Fatal(err)
		}
	}
	final, err := p.Finalize(ctx, leader)
	if err != nil || !final.IsNull() || calls != 1 || leader.(*transitionAggregateState).unseeded {
		t.Fatal("strict returned NULL reseeded", final, calls, err)
	}
	cp.Support = &catalog.Procedure{}
	calls = 0
	cp.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(c expr.Context) (sqltype.Value, error) {
		calls++
		args := c.(*Context).Parameters
		if !args[0].IsNull() || !args[1].IsNull() {
			t.Fatal("nonstrict NULL operands lost")
		}
		return sqltype.Null(d), nil
	}}
	leader, _ = p.Init(ctx)
	for range 2 {
		if err := p.Combine(ctx, leader, incoming(sqltype.Null(d)), evaluation); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	owner.Aggregate.Definition.HasInitial, owner.Aggregate.Definition.Initial = true, "10"
	p.HasInitial, p.Initial, cp.HasInitial, cp.Initial = true, "10", true, "10"
	cp.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(c expr.Context) (sqltype.Value, error) {
		args := c.(*Context).Parameters
		left, _ := args[0].Integer()
		right, _ := args[1].Integer()
		return sqltype.Integer(d, left+right)
	}}
	leader, _ = p.Init(ctx)
	for range 2 {
		partial, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Combine(ctx, leader, partial, evaluation); err != nil {
			t.Fatal(err)
		}
	}
	final, err = p.Finalize(ctx, leader)
	n, integerErr := final.Integer()
	if err != nil || integerErr != nil || n != 30 {
		t.Fatal("leader/partial seeds not independently initialized", n, err, integerErr)
	}
	canceled, cancel := context.WithCancel(ctx)
	cp.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(expr.Context) (sqltype.Value, error) { cancel(); return value(999), nil }}
	before := leader.(*transitionAggregateState).value
	if err := p.Combine(canceled, leader, incoming(value(1)), &Context{EvalStatement: EvalStatement{Context: canceled}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if leader.(*transitionAggregateState).value != before {
		t.Fatal("canceled combine published state")
	}
}

// Partial combination consumes transition values, not the distinct result of a
// final function. Only the leader invokes that final function.
func TestAggregateCombineDistinctFinalResult(t *testing.T) {
	ctx := context.Background()
	stateType := transitionUnitType(t)
	var resultType *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			resultType = typ
			break
		}
	}
	if resultType == nil {
		t.Fatal("missing text")
	}
	u := stateType.Usage()
	conversion, err := sqltype.PlanCoercion(u, u, sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	owner := &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}
	combine := &TransitionAggregate{Aggregate: owner, Support: &catalog.Procedure{Strict: true}, StateUsage: u, Result: u, Inputs: []sqltype.Usage{u, u}, Formals: []sqltype.Usage{u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion}, SeedConversion: conversion}
	combine.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(c expr.Context) (sqltype.Value, error) {
		values := c.(*Context).Parameters
		left, _ := values[0].Integer()
		right, _ := values[1].Integer()
		return sqltype.Integer(stateType, left+right)
	}}
	provider := *combine
	provider.Result = resultType.Usage()
	provider.CombinePlan = combine
	provider.Final = &catalog.Procedure{}
	provider.FinalInputs, provider.FinalFormals = []sqltype.Usage{u}, []sqltype.Usage{u}
	provider.FinalConversions = []sqltype.CoercionPlan{conversion}
	finals := 0
	provider.FinalCall = transitionUnitExpr{Info: expr.Info{Usage: resultType.Usage()}, run: func(c expr.Context) (sqltype.Value, error) {
		finals++
		value, err := c.(*Context).Parameters[0].Integer()
		if err != nil || value != 30 {
			t.Fatalf("final input=%d/%v", value, err)
		}
		return sqltype.String(resultType, "thirty"), nil
	}}
	leader, err := provider.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	for _, n := range []int64{5, 25} {
		value, err := sqltype.Integer(stateType, n)
		if err != nil {
			t.Fatal(err)
		}
		partial := &transitionAggregateState{owner: &provider, value: value}
		if err := provider.Combine(ctx, leader, partial, evaluation); err != nil {
			t.Fatal(err)
		}
	}
	if finals != 0 {
		t.Fatal("worker finalized transition state")
	}
	result, err := provider.FinalizeWithContext(ctx, leader, nil, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	text, err := result.Text()
	if err != nil || text != "thirty" || result.Type() != resultType || finals != 1 {
		t.Fatalf("leader result=%q/%v finals=%d", text, err, finals)
	}
	state, err := leader.(*transitionAggregateState).value.Integer()
	if err != nil || state != 30 {
		t.Fatal("final replaced transition state", state, err)
	}
}

func TestAggregateParallelOperationContext(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "request"))
	defer cancel()
	if InParallelOperation(nil) || InParallelWorker(nil) || InParallelOperation(parent) || InParallelWorker(parent) {
		t.Fatal("ordinary request acquired parallel identity")
	}
	operation := ParallelOperationContext(parent)
	if !InParallelOperation(operation) || InParallelWorker(operation) || operation.Value(key{}) != "request" {
		t.Fatal("leader operation identity or request value lost")
	}
	if ParallelOperationContext(operation) != operation {
		t.Fatal("already marked operation allocated a replacement context")
	}
	worker := ParallelWorkerContext(operation)
	if !InParallelWorker(worker) || !InParallelOperation(worker) || worker.Value(key{}) != "request" {
		t.Fatal("worker lost operation identity or request value")
	}
	directWorker := ParallelWorkerContext(parent)
	if !InParallelWorker(directWorker) || !InParallelOperation(directWorker) {
		t.Fatal("direct worker context did not imply a parallel operation")
	}
	if ParallelWorkerContext(worker) != worker || ParallelOperationContext(worker) != worker {
		t.Fatal("already marked worker allocated a replacement context")
	}
	state := &State{ExecutionContext: operation}
	evaluation := state.Evaluation(nil)
	if !InParallelOperation(evaluation.Context) || InParallelWorker(evaluation.Context) {
		t.Fatal("initial leader evaluator lost operation identity")
	}
	cancel()
	for _, ctx := range []context.Context{operation, worker, directWorker} {
		if !errors.Is(ctx.Err(), context.Canceled) || !InParallelOperation(ctx) {
			t.Fatal("parallel marker detached request cancellation", ctx.Err())
		}
	}
	if InParallelOperation(parent) || InParallelWorker(parent) {
		t.Fatal("parallel identity escaped into ordinary request")
	}
}

func TestAggregateParallelOperationSequenceGuard(t *testing.T) {
	leader := ParallelOperationContext(context.Background())
	for _, phase := range []struct {
		name string
		ctx  context.Context
	}{{"leader", leader}, {"worker", ParallelWorkerContext(leader)}} {
		for _, name := range []string{"nextval", "setval"} {
			t.Run(phase.name+"/"+name, func(t *testing.T) {
				evaluation := &Context{EvalStatement: EvalStatement{Context: phase.ctx}}
				_, err := evaluation.Sequence(functions.SequenceRequest{Name: name})
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "25000" || diagnostic.Message != "cannot execute "+name+"() during a parallel operation" {
					t.Fatal("parallel effect reached statement host", err)
				}
			})
		}
	}
}
