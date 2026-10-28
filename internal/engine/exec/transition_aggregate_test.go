package exec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
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

// A private scalar provider isolates strict transition-returned-NULL behavior
// from binding and public SQL execution.
func TestCreateAggregateStrictReturnedNullDoesNotReseed(t *testing.T) {
	d := transitionUnitType(t)
	u := d.Usage()
	conversion, err := sqltype.PlanCoercion(u, u, sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := &TransitionAggregate{Aggregate: &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}, Support: &catalog.Procedure{Strict: true}, StateUsage: u, Result: u, Inputs: []sqltype.Usage{u, u, u}, Formals: []sqltype.Usage{u, u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion, conversion}, SeedConversion: conversion}
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
	provider := &TransitionAggregate{Aggregate: &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}, Support: &catalog.Procedure{}, StateUsage: u, Result: u, Inputs: []sqltype.Usage{u, u}, Formals: []sqltype.Usage{u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion}}
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
	provider := &TransitionAggregate{Aggregate: &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{}}, Support: &catalog.Procedure{}, StateUsage: u, Result: u, Inputs: []sqltype.Usage{u, u}, Formals: []sqltype.Usage{u, u}, Conversions: []sqltype.CoercionPlan{conversion, conversion}}
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

func finalUnitProvider(t *testing.T) (*TransitionAggregate, *sqltype.Descriptor) {
	t.Helper()
	integer := transitionUnitType(t)
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
		}
	}
	if text == nil {
		t.Fatal("missing text")
	}
	inputs := []sqltype.Usage{integer.Usage(), integer.Usage(), text.Usage()}
	conversions := make([]sqltype.CoercionPlan, len(inputs))
	for i, usage := range inputs {
		var err error
		conversions[i], err = sqltype.PlanCoercion(usage, usage, sqltype.Implicit)
		if err != nil {
			t.Fatal(err)
		}
	}
	final := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90002}, ObjectRevision: 1, ObjectName: "unit_final", Signature: "23,23,25", RoutineKind: catalog.FunctionRoutine, Result: 25}
	p := &TransitionAggregate{
		Aggregate: &catalog.Procedure{Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Functions: [8]ids.OID{0, 90002}, HasInitial: true, Initial: "0", FinalExtra: true, FinalModify: "w"}}},
		Support:   &catalog.Procedure{}, StateUsage: integer.Usage(), Result: text.Usage(),
		Initial: "0", HasInitial: true, FinalModify: "w",
		Inputs: inputs, Formals: append([]sqltype.Usage(nil), inputs...), Conversions: conversions,
		Final: final, FinalInputs: append([]sqltype.Usage(nil), inputs...), FinalFormals: append([]sqltype.Usage(nil), inputs...), FinalConversions: append([]sqltype.CoercionPlan(nil), conversions...),
	}
	p.Call = transitionUnitExpr{Info: expr.Info{Usage: integer.Usage()}, run: func(c expr.Context) (sqltype.Value, error) {
		values := c.(*Context).Parameters
		state, err := values[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if !values[1].IsNull() {
			n, err := values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			state += n
		}
		return sqltype.Integer(integer, state)
	}}
	p.FinalCall = transitionUnitExpr{Info: expr.Info{Usage: text.Usage(), RoutineReference: final}, run: func(c expr.Context) (sqltype.Value, error) {
		values := c.(*Context).Parameters
		if len(values) != 3 || values[0].Type() != integer || values[1].Type() != integer || values[2].Type() != text || !values[1].IsNull() || !values[2].IsNull() {
			return sqltype.Value{}, fmt.Errorf("incorrect final typed NULL vector: %v", values)
		}
		if values[0].IsNull() {
			return sqltype.String(text, "empty"), nil
		}
		n, err := values[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(text, fmt.Sprintf("total=%d", n)), nil
	}}
	return p, text
}

func TestAggregateFinalFrame(t *testing.T) {
	for _, kind := range []string{"state", "null-state", "strict-null", "returned-null", "wrong-result", "failure"} {
		t.Run(kind, func(t *testing.T) {
			p, text := finalUnitProvider(t)
			ctx := context.Background()
			evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
			if kind == "null-state" || kind == "strict-null" {
				p.Aggregate.Aggregate.Definition.HasInitial = false
				p.HasInitial = false
			}
			if kind == "strict-null" {
				p.Final.Strict = true
				p.Aggregate.Aggregate.Definition.FinalExtra = false
				p.Final.Signature = "23"
				p.FinalInputs, p.FinalFormals, p.FinalConversions = p.FinalInputs[:1], p.FinalFormals[:1], p.FinalConversions[:1]
				p.FinalCall = transitionUnitExpr{Info: expr.Info{Usage: text.Usage()}, run: func(expr.Context) (sqltype.Value, error) {
					t.Fatal("strict NULL final was invoked")
					return sqltype.Value{}, nil
				}}
			}
			sentinel := errors.New("final failed")
			if kind == "returned-null" || kind == "wrong-result" || kind == "failure" {
				p.FinalCall = transitionUnitExpr{Info: expr.Info{Usage: text.Usage()}, run: func(expr.Context) (sqltype.Value, error) {
					switch kind {
					case "returned-null":
						return sqltype.Null(text), nil
					case "wrong-result":
						return sqltype.Null(p.StateUsage.Type), nil
					default:
						return sqltype.Value{}, sentinel
					}
				}}
			}
			opaque, err := p.InitWithContext(ctx, evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "state" {
				for _, n := range []int64{1, 2, 3} {
					value, _ := sqltype.Integer(p.StateUsage.Type, n)
					if err := p.Advance(ctx, opaque, []sqltype.Value{value, sqltype.String(text, "last row")}, evaluation); err != nil {
						t.Fatal(err)
					}
				}
			}
			state := opaque.(*transitionAggregateState)
			before := state.value
			result, err := p.FinalizeWithContext(ctx, opaque, nil, evaluation)
			if kind == "wrong-result" || kind == "failure" {
				if err == nil || kind == "failure" && !errors.Is(err, sentinel) {
					t.Fatal("invalid final accepted", result, err)
				}
			} else {
				if err != nil || result.Type() != text {
					t.Fatal("distinct final result lost", result, err)
				}
				if kind == "strict-null" || kind == "returned-null" {
					if !result.IsNull() {
						t.Fatal("final NULL lost", result)
					}
				} else {
					want := "total=6"
					if kind == "null-state" {
						want = "empty"
					}
					got, err := result.Text()
					if err != nil || got != want {
						t.Fatal(got, err)
					}
				}
			}
			if state.value != before {
				t.Fatal("final overwrote transition state")
			}
			if _, err := p.Finalize(ctx, opaque); err == nil {
				t.Fatal("context-free entry silently returned state")
			}
		})
	}
}

func TestAggregateFinalCancellation(t *testing.T) {
	p, text := finalUnitProvider(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evaluation := &Context{EvalStatement: EvalStatement{Context: ctx}}
	opaque, err := p.InitWithContext(ctx, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	before := opaque.(*transitionAggregateState).value
	p.FinalCall = transitionUnitExpr{Info: expr.Info{Usage: text.Usage()}, run: func(expr.Context) (sqltype.Value, error) {
		cancel()
		return sqltype.String(text, "not published"), nil
	}}
	if value, err := p.FinalizeWithContext(ctx, opaque, nil, evaluation); !errors.Is(err, context.Canceled) || value.Type() != nil {
		t.Fatal("canceled final published output", value, err)
	}
	if opaque.(*transitionAggregateState).value != before {
		t.Fatal("canceled final changed state")
	}
	if _, err := p.FinalizeWithContext(ctx, opaque, nil, evaluation); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled final admitted", err)
	}
}

func TestAggregateFinalCallIdentity(t *testing.T) {
	for _, kind := range []string{"valid", "same-shape-address", "changed-declaration", "missing-reference", "zero-revision", "wrong-result", "missing-provider", "wrong-slot", "extra-frame"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := finalUnitProvider(t)
			call := p.FinalCall.(transitionUnitExpr)
			declaration := *p.Final
			call.RoutineReference = &declaration
			switch kind {
			case "same-shape-address":
				declaration.ObjectAddress.ObjectID++
			case "changed-declaration":
				declaration.Strict = true
			case "missing-reference":
				call.RoutineReference = nil
			case "zero-revision":
				declaration.ObjectRevision = 0
			case "wrong-result":
				call.Usage = p.StateUsage
			case "wrong-slot":
				p.Aggregate.Aggregate.Definition.Functions[1]++
			case "extra-frame":
				p.FinalInputs = append(p.FinalInputs, p.StateUsage)
			}
			p.FinalCall = call
			if kind == "missing-provider" {
				p.FinalCall = nil
			}
			if err := p.validateFinalFrame(); (err == nil) != (kind == "valid") {
				t.Fatal(kind, err)
			}
		})
	}
}

func TestAggregateFinalPrivateState(t *testing.T) {
	p, text := finalUnitProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan *Context, 2)
	release := make(chan struct{})
	original := p.FinalCall.(transitionUnitExpr)
	p.FinalCall = transitionUnitExpr{Info: original.Info, run: func(c expr.Context) (sqltype.Value, error) {
		entered <- c.(*Context)
		<-release
		return original.Eval(c)
	}}
	states := make([]any, 2)
	evaluations := make([]*Context, 2)
	for i := range states {
		evaluations[i] = &Context{EvalStatement: EvalStatement{Context: ctx, Parameters: []sqltype.Value{sqltype.String(text, "outer")}}}
		var err error
		states[i], err = p.InitWithContext(ctx, evaluations[i])
		if err != nil {
			t.Fatal(err)
		}
		v, _ := sqltype.Integer(p.StateUsage.Type, int64(i+3))
		if err := p.Advance(ctx, states[i], []sqltype.Value{v, sqltype.String(text, "input")}, evaluations[i]); err != nil {
			t.Fatal(err)
		}
	}
	a, b := states[0].(*transitionAggregateState), states[1].(*transitionAggregateState)
	if a.frame == b.frame || a.finalFrame == b.finalFrame || a.frame == a.finalFrame {
		t.Fatal("groups share mutable frames")
	}
	var wg sync.WaitGroup
	results := make([]sqltype.Value, 2)
	errs := make([]error, 2)
	for i := range states {
		wg.Go(func() { results[i], errs[i] = p.FinalizeWithContext(ctx, states[i], nil, evaluations[i]) })
	}
	var frames [2]*Context
	for i := range frames {
		select {
		case frames[i] = <-entered:
		case <-ctx.Done():
			close(release)
			wg.Wait()
			t.Fatal("concurrent final did not reach private frame", ctx.Err(), errs)
		}
	}
	first, second := frames[0], frames[1]
	if first == second || &first.Parameters[0] == &second.Parameters[0] || first == evaluations[0] || second == evaluations[1] {
		close(release)
		wg.Wait()
		t.Fatal("concurrent finals share an execution frame")
	}
	close(release)
	wg.Wait()
	for i := range results {
		got, err := results[i].Text()
		if errs[i] != nil || err != nil || got != fmt.Sprintf("total=%d", i+3) {
			t.Fatal("independent final result", i, got, err, errs[i])
		}
		if outer, _ := evaluations[i].Parameters[0].Text(); outer != "outer" {
			t.Fatal("final replaced enclosing parameter namespace")
		}
	}
	other := *p
	if _, err := other.FinalizeWithContext(ctx, states[0], nil, evaluations[0]); err == nil {
		t.Fatal("another declaration consumed private state")
	}
	opaque, err := p.InitWithContext(ctx, evaluations[0])
	if err != nil {
		t.Fatal(err)
	}
	state := opaque.(*transitionAggregateState)
	if state.frame == a.frame || state.finalFrame == a.finalFrame {
		t.Fatal("next execution reused a retained group frame")
	}
	if got, _ := results[0].Text(); got != "total=3" {
		t.Fatal("retained output mutated", got)
	}
}

type finalUnitSQLHost struct {
	Host
	name  string
	usage sqltype.Usage
	input sqltype.InputEnvironment
}

func (h finalUnitSQLHost) Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment) {
	return h.input, sqltype.OutputEnvironment{}
}

func (h finalUnitSQLHost) InvokeAggregateSQLRoutine(ctx context.Context, _ *catalog.Procedure, frame *proc.Frame, usage sqltype.Usage, _ *catalog.Procedure, _ sqltype.CollationRef) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if usage != h.usage || len(frame.Parameters) != 3 || frame.Parameters[0].IsNull() || !frame.Parameters[1].IsNull() || !frame.Parameters[2].IsNull() {
		return sqltype.Value{}, fmt.Errorf("invalid SQL final frame/result usage")
	}
	return sqltype.String(usage.Type, h.name), nil
}

func TestAggregateFinalCurrentHost(t *testing.T) {
	p, text := finalUnitProvider(t)
	p.Final.SQL, p.FinalCall = &proc.Definition{}, nil
	ctx := context.Background()
	first := &Context{EvalStatement: EvalStatement{Context: ctx, Host: finalUnitSQLHost{name: "first", usage: text.Usage(), input: sqltype.InputEnvironment{XMLMode: sqltype.XMLContent}}}}
	second := &Context{EvalStatement: EvalStatement{Context: ctx, Host: finalUnitSQLHost{name: "second", usage: text.Usage(), input: sqltype.InputEnvironment{XMLMode: sqltype.XMLDocument}}}}
	state, err := p.InitWithContext(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, evaluation := range []*Context{second, first, second} {
		result, err := p.FinalizeWithContext(ctx, state, nil, evaluation)
		if err != nil {
			t.Fatal(err)
		}
		got, err := result.Text()
		if err != nil || got != evaluation.Host.(finalUnitSQLHost).name {
			t.Fatal("final retained initialization/previous host", got, err)
		}
		if evaluation.FunctionInput().XMLMode != evaluation.Host.(finalUnitSQLHost).input.XMLMode {
			t.Fatal("final retained initialization/previous host environment")
		}
	}
}
