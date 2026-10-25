package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func movingUnitProvider(t *testing.T, strict, seed bool) *TransitionAggregate {
	t.Helper()
	u := transitionUnitType(t).Usage()
	conversion, err := catalog.AggregateConversion(nil, u, u)
	if err != nil {
		t.Fatal(err)
	}
	forward := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98001}, ObjectRevision: 1, ObjectName: "moving_forward", Signature: "23,23", Result: 23, Strict: strict}
	inverse := *forward
	inverse.ObjectAddress.ObjectID, inverse.ObjectName = 98002, "moving_inverse"
	p := &TransitionAggregate{Moving: true, HasInitial: seed, FinalModify: "r", Support: forward, InverseSupport: &inverse, StateUsage: u, Result: u, Inputs: []sqltype.Usage{u, u}, Formals: []sqltype.Usage{u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion}, InverseFormals: []sqltype.Usage{u, u}, InverseConversions: []sqltype.CoercionPlan{conversion, conversion}, SeedConversion: conversion}
	if seed {
		p.Initial = "0"
	}
	p.Aggregate = &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98003}, Result: 23, Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Functions: [8]ids.OID{0, 0, 0, 0, 0, 98001, 98002}, MovingType: 23, HasMovingInitial: seed, MovingInitial: p.Initial, MovingFinalModify: "r"}}}
	p.Call = transitionUnitExpr{Info: expr.Info{Usage: u, RoutineReference: forward}, run: func(c expr.Context) (sqltype.Value, error) {
		v := c.(*Context).Parameters
		a, err := v[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		b, err := v[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Integer(u.Type, a+b)
	}}
	p.InverseCall = transitionUnitExpr{Info: expr.Info{Usage: u, RoutineReference: &inverse}, run: func(c expr.Context) (sqltype.Value, error) {
		v := c.(*Context).Parameters
		a, err := v[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		b, err := v[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Integer(u.Type, a-b)
	}}
	return p
}

func movingUnitValue(t *testing.T, p *TransitionAggregate, n int64) sqltype.Value {
	t.Helper()
	v, err := sqltype.Integer(p.StateUsage.Type, n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAggregateMovingTransition(t *testing.T) {
	for _, strict := range []bool{false, true} {
		p := movingUnitProvider(t, strict, !strict)
		ctx := context.Background()
		evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
		opaque, err := p.InitWithContext(ctx, evaluation)
		if err != nil {
			t.Fatal(err)
		}
		state := opaque.(*transitionAggregateState)
		advance := func(n int64) {
			if err := p.Advance(ctx, state, []sqltype.Value{movingUnitValue(t, p, n)}, evaluation); err != nil {
				t.Fatal(err)
			}
		}
		assert := func(n int64, count uint64) {
			got, err := state.value.Integer()
			if err != nil || got != n || state.contributed != count {
				t.Fatal(got, state.contributed, err)
			}
		}
		advance(2)
		assert(2, 1)
		advance(3)
		assert(5, 2)
		if strict {
			if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(p.StateUsage.Type)}, evaluation); err != nil {
				t.Fatal(err)
			}
			if punt, err := p.Inverse(ctx, state, []sqltype.Value{sqltype.Null(p.StateUsage.Type)}, evaluation); punt || err != nil {
				t.Fatal(punt, err)
			}
			assert(5, 2)
		}
		if punt, err := p.Inverse(ctx, state, []sqltype.Value{movingUnitValue(t, p, 2)}, evaluation); punt || err != nil {
			t.Fatal(punt, err)
		}
		assert(3, 1)
		advance(5)
		assert(8, 2)
		before := state.value
		p.InverseCall = transitionUnitExpr{Info: expr.Info{Usage: p.StateUsage}, run: func(expr.Context) (sqltype.Value, error) { return sqltype.Null(p.StateUsage.Type), nil }}
		if punt, err := p.Inverse(ctx, state, []sqltype.Value{movingUnitValue(t, p, 3)}, evaluation); !punt || err != nil || state.value != before || state.contributed != 2 {
			t.Fatal("inverse NULL was committed", punt, err, state)
		}
		// Last contributed removal resets without evaluating the inverse body.
		state.contributed = 1
		p.InverseCall = transitionUnitExpr{Info: expr.Info{Usage: p.StateUsage}, run: func(expr.Context) (sqltype.Value, error) {
			t.Fatal("last row called inverse")
			return sqltype.Value{}, nil
		}}
		if punt, err := p.Inverse(ctx, state, []sqltype.Value{movingUnitValue(t, p, 3)}, evaluation); punt || err != nil || state.contributed != 0 {
			t.Fatal(punt, err)
		}
		if strict {
			if !state.value.IsNull() || !state.unseeded {
				t.Fatal("strict reset lost NULL seed")
			}
		} else {
			assert(0, 0)
		}
	}
	p := movingUnitProvider(t, false, true)
	ctx := context.Background()
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	state, err := p.InitWithContext(ctx, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	p.Call = transitionUnitExpr{Info: expr.Info{Usage: p.StateUsage}, run: func(expr.Context) (sqltype.Value, error) { return sqltype.Null(p.StateUsage.Type), nil }}
	err = p.Advance(ctx, state, []sqltype.Value{movingUnitValue(t, p, 2)}, evaluation)
	if diagnostic, ok := err.(interface{ SQLState() string }); !ok || diagnostic.SQLState() != "22004" {
		t.Fatal("moving forward NULL accepted", err)
	}
	if got, _ := state.(*transitionAggregateState).value.Integer(); got != 0 {
		t.Fatal("failed forward replaced seed", got)
	}
}

func TestAggregateMovingCancellation(t *testing.T) {
	for _, phase := range []string{"init", "forward", "inverse", "final"} {
		t.Run(phase, func(t *testing.T) {
			p := movingUnitProvider(t, false, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
			if phase == "init" {
				cancel()
				if _, err := p.InitWithContext(ctx, evaluation); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				return
			}
			opaque, err := p.InitWithContext(ctx, evaluation)
			if err != nil {
				t.Fatal(err)
			}
			state := opaque.(*transitionAggregateState)
			for _, n := range []int64{2, 3} {
				if err := p.Advance(ctx, state, []sqltype.Value{movingUnitValue(t, p, n)}, evaluation); err != nil {
					t.Fatal(err)
				}
			}
			before, count := state.value, state.contributed
			interrupted := transitionUnitExpr{Info: expr.Info{Usage: p.StateUsage}, run: func(expr.Context) (sqltype.Value, error) { cancel(); return movingUnitValue(t, p, 99), nil }}
			switch phase {
			case "forward":
				p.Call = interrupted
				err = p.Advance(ctx, state, []sqltype.Value{movingUnitValue(t, p, 5)}, evaluation)
			case "inverse":
				p.InverseCall = interrupted
				_, err = p.Inverse(ctx, state, []sqltype.Value{movingUnitValue(t, p, 2)}, evaluation)
			case "final":
				p.Final = &catalog.Procedure{}
				p.FinalFormals = []sqltype.Usage{p.StateUsage}
				p.FinalInputs = p.FinalFormals
				p.FinalConversions = p.Conversions[:1]
				p.FinalCall = interrupted
				state.finalFrame = state.frame
				var result sqltype.Value
				result, err = p.FinalizeWithContext(ctx, state, evaluation)
				if result.Type() != nil {
					t.Fatal("canceled final exposed result")
				}
			}
			if !errors.Is(err, context.Canceled) || state.value != before || state.contributed != count {
				t.Fatal("canceled support changed state", phase, err)
			}
		})
	}
}

func TestAggregateMovingWindowState(t *testing.T) {
	// Empty partitions require no source rows, but still exercise actual selected
	// contextual initialization/finalization and independent occurrence ownership.
	p := movingUnitProvider(t, false, true)
	ctx := context.Background()
	state := &State{}
	call := plan.WindowCall{Operation: plan.WindowAggregate, Provider: p, Expression: &plan.WindowExpr{Info: expr.Info{Usage: p.Result}}}
	calls := []plan.WindowCall{call, call}
	stage := WindowAggregates{States: []WindowAggregateState{{CallIndex: 0}, {CallIndex: 1}}}
	frame := plan.WindowFrame{Start: plan.WindowBound{Kind: parser.FrameCurrentRow}, End: plan.WindowBound{Kind: parser.FrameCurrentRow}}
	if err := stage.Evaluate(ctx, state, &Stage{}, calls, nil, frame, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	a, b := stage.States[0].Aggregate.(*transitionAggregateState), stage.States[1].Aggregate.(*transitionAggregateState)
	if a == b || a.frame == b.frame || a.inverseFrame == b.inverseFrame {
		t.Fatal("window occurrences share mutable state")
	}
	stage.Initialized = false
	if err := stage.Evaluate(ctx, state, &Stage{}, calls, nil, frame, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if stage.States[0].Aggregate == a {
		t.Fatal("partition retained transition state")
	}
}
