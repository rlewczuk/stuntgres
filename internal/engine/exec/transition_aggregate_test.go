package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type transitionUnitExpr struct {
	expr.Info
	run func(expr.Context) (sqltype.Value, error)
}

func (e transitionUnitExpr) Eval(c expr.Context) (sqltype.Value, error) { return e.run(c) }

func transitionUnitType(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			return d
		}
	}
	t.Fatal("missing int4")
	return nil
}

// No public SQL surface can declare a strict SQL function in this part. This
// private scalar provider supplies the genuine transition-returned-NULL path,
// not an alternate state machine or a public CREATE FUNCTION attribute.
func TestCreateAggregateStrictReturnedNullDoesNotReseed(t *testing.T) {
	d := transitionUnitType(t)
	u := d.Usage()
	conversion, err := sqltype.PlanCoercion(u, u, sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := &TransitionAggregate{Aggregate: &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}, Support: &catalog.Procedure{Strict: true}, Usage: u, Inputs: []sqltype.Usage{u, u, u}, Formals: []sqltype.Usage{u, u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion, conversion}, SeedConversion: conversion}
	provider.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(c expr.Context) (sqltype.Value, error) {
		calls++
		values := c.(*Context).Parameters
		for i, want := range []int64{2, 3, 20} {
			got, err := values[i].Integer()
			if err != nil || got != want {
				t.Fatalf("support input%d=%v/%v want%d", i, got, err, want)
			}
		}
		return sqltype.Null(d), nil
	}}
	ctx := context.Background()
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	opaque, err := provider.InitWithContext(ctx, evaluation)
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
	for _, row := range [][]sqltype.Value{{value(7), sqltype.Null(d)}, {value(2), value(10)}, {value(3), value(20)}, {value(4), value(30)}} {
		if err = provider.Advance(ctx, opaque, row, evaluation); err != nil {
			t.Fatal(err)
		}
	}
	result, err := provider.Finalize(ctx, opaque)
	if err != nil || !result.IsNull() || result.Type() != d || calls != 1 {
		t.Fatalf("result=%v/%v calls=%d", result, err, calls)
	}
	if opaque.(*transitionAggregateState).unseeded {
		t.Fatal("transition NULL restored unseeded flag")
	}
}

func TestCreateAggregateNonstrictNullArgumentsAndState(t *testing.T) {
	d := transitionUnitType(t)
	u := d.Usage()
	conversion, _ := sqltype.PlanCoercion(u, u, sqltype.Implicit)
	calls := 0
	provider := &TransitionAggregate{Aggregate: &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}, Support: &catalog.Procedure{}, Usage: u, Inputs: []sqltype.Usage{u, u}, Formals: []sqltype.Usage{u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion}}
	provider.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(c expr.Context) (sqltype.Value, error) {
		calls++
		if values := c.(*Context).Parameters; !values[0].IsNull() || !values[1].IsNull() {
			t.Fatal("nonstrict NULL inputs lost", values)
		}
		return sqltype.Null(d), nil
	}}
	ctx := context.Background()
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	opaque, err := provider.InitWithContext(ctx, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = provider.Advance(ctx, opaque, []sqltype.Value{sqltype.Null(d)}, evaluation); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 || opaque.(*transitionAggregateState).unseeded {
		t.Fatalf("calls=%d state=%#v", calls, opaque)
	}
}

func TestCreateAggregateTransitionCancellation(t *testing.T) {
	d := transitionUnitType(t)
	u := d.Usage()
	conversion, _ := sqltype.PlanCoercion(u, u, sqltype.Implicit)
	ctx, cancel := context.WithCancel(context.Background())
	provider := &TransitionAggregate{Aggregate: &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}, Support: &catalog.Procedure{}, Usage: u, Inputs: []sqltype.Usage{u, u}, Formals: []sqltype.Usage{u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion}}
	provider.Call = transitionUnitExpr{Info: expr.Info{Usage: u}, run: func(expr.Context) (sqltype.Value, error) { cancel(); return sqltype.Integer(d, 9) }}
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	opaque, err := provider.InitWithContext(ctx, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(d, 1)
	if err = provider.Advance(ctx, opaque, []sqltype.Value{one}, evaluation); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !opaque.(*transitionAggregateState).value.IsNull() {
		t.Fatal("canceled result committed to transition state")
	}
	if _, err = provider.Finalize(ctx, opaque); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = provider.InitWithContext(ctx, evaluation); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// These are declaration carriers, not executable catalog fixtures. The stored
// remapper separately validates the complete destination aggregate/support.
func TestCreateAggregateTransitionCallIdentity(t *testing.T) {
	usage := transitionUnitType(t).Usage()
	support := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90000}, ObjectRevision: 1, ObjectName: "selected_support", Signature: "23,23", RoutineKind: catalog.FunctionRoutine, Result: 23}
	for _, kind := range []string{"valid", "same-shape-different-identity", "changed-declaration", "missing-reference", "zero-revision", "wrong-result", "missing-provider"} {
		t.Run(kind, func(t *testing.T) {
			declaration := *support
			call := transitionUnitExpr{Info: expr.Info{Usage: usage, RoutineReference: &declaration}}
			switch kind {
			case "same-shape-different-identity":
				declaration.ObjectAddress.ObjectID++
			case "changed-declaration":
				declaration.Signature = "21,21"
			case "missing-reference":
				call.RoutineReference = nil
			case "zero-revision":
				declaration.ObjectRevision = 0
			case "wrong-result":
				call.Usage = sqltype.Usage{}
			}
			var node expr.Expr = call
			if kind == "missing-provider" {
				node = nil
			}
			err := validateTransitionCall(node, support, usage)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s selected support identity=%v", kind, err)
			}
		})
	}
}
