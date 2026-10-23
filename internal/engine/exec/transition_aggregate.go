package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TransitionAggregate is immutable. It borrows the actual statement runtime at
// execution; no engine/session pointer or mutable transition frame is retained.
type TransitionAggregate struct {
	Aggregate, Support *catalog.Procedure
	Usage              sqltype.Usage
	InputCollation     sqltype.CollationRef // Effective common input; zero for indeterminate/noncollatable input.
	Inputs, Formals    []sqltype.Usage      // state followed by fixed ordinary inputs
	Conversions        []sqltype.CoercionPlan
	SeedConversion     sqltype.CoercionPlan
	Call               expr.Expr // Existing scalar provider over a private parameter frame.
}

type transitionAggregateState struct {
	value    sqltype.Value
	unseeded bool
	frame    *proc.Frame
}

func (p *TransitionAggregate) ResultUsage() sqltype.Usage { return p.Usage }
func (p *TransitionAggregate) Init(ctx context.Context) (any, error) {
	return p.initialize(ctx, sqltype.InputEnvironment{})
}
func (p *TransitionAggregate) InitWithContext(ctx context.Context, evaluation expr.Context) (any, error) {
	if evaluation == nil {
		return nil, fmt.Errorf("stuntgres: transition aggregate has no execution context")
	}
	return p.initialize(ctx, evaluation.FunctionInput())
}

// Start checks live strict-seed feasibility once at executor startup, not at
// Prepare, per group, or when the first non-NULL row arrives.
func (p *TransitionAggregate) Start(c *Context) error {
	if !p.Support.Strict || p.Aggregate.Aggregate.Definition.HasInitial {
		return nil
	}
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	if _, compatible := catalog.ImplicitBinaryCast(auth.Catalog, p.Inputs[1].Type.ID(), p.Usage.Type.ID()); !compatible {
		return expr.ErrorAt("42P13", fmt.Sprintf("aggregate %d needs to have compatible input type and transition type", p.Aggregate.Address().ObjectID), parser.Position{})
	}
	return nil
}
func (p *TransitionAggregate) initialize(ctx context.Context, input sqltype.InputEnvironment) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &transitionAggregateState{value: sqltype.Null(p.Usage.Type), unseeded: !p.Aggregate.Aggregate.Definition.HasInitial, frame: proc.NewFrame(p.Formals)}
	if p.Aggregate.Aggregate.Definition.HasInitial {
		value, err := p.Usage.Type.InputWithEnvironment(ctx, p.Aggregate.Aggregate.Definition.Initial, p.Usage.Typmod, input)
		if err != nil {
			return nil, err
		}
		state.value = value
	}
	return state, ctx.Err()
}

func (p *TransitionAggregate) Advance(ctx context.Context, opaque any, values []sqltype.Value, evaluation expr.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, ok := opaque.(*transitionAggregateState)
	if !ok || state == nil || len(values)+1 != len(p.Inputs) {
		return fmt.Errorf("stuntgres: invalid transition aggregate state or arguments")
	}
	for i, value := range values {
		if value.Type() != p.Inputs[i+1].Type {
			return fmt.Errorf("stuntgres: foreign transition aggregate input")
		}
	}
	if p.Support.Strict {
		for _, value := range values {
			if value.IsNull() {
				return ctx.Err()
			}
		}
		if state.unseeded {
			value, err := p.SeedConversion.Apply(ctx, values[0], evaluation.FunctionInput(), evaluation.FunctionOutput())
			if err != nil {
				return err
			}
			state.value, state.unseeded = value, false
			return ctx.Err()
		}
		if state.value.IsNull() {
			return ctx.Err()
		}
	}
	runtime, ok := evaluation.(*Context)
	if !ok {
		return fmt.Errorf("stuntgres: transition aggregate has no statement runtime")
	}
	local := *runtime
	for i := range p.Inputs {
		value := state.value
		if i > 0 {
			value = values[i-1]
		}
		converted, err := p.Conversions[i].Apply(ctx, value, local.Input, local.Output)
		if err != nil {
			return err
		}
		if err = state.frame.Bind(i, converted, p.Formals[i]); err != nil {
			return err
		}
	}
	var result sqltype.Value
	var err error
	if p.Support.SQL != nil {
		host, ok := local.LiveHost().(interface {
			InvokeAggregateSQLRoutine(*catalog.Procedure, *proc.Frame, sqltype.Usage, *catalog.Procedure, sqltype.CollationRef) (sqltype.Value, error)
		})
		if !ok {
			return fmt.Errorf("stuntgres: aggregate support invocation has no statement owner")
		}
		result, err = host.InvokeAggregateSQLRoutine(p.Support, state.frame, p.Usage, p.Aggregate, p.InputCollation)
	} else {
		local.Parameters = state.frame.Parameters
		result, err = p.Call.Eval(&local)
	}
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if result.Type() != p.Usage.Type {
		return fmt.Errorf("stuntgres: invalid transition aggregate result")
	}
	state.value, state.unseeded = result, false
	return nil
}
func (p *TransitionAggregate) Finalize(ctx context.Context, opaque any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	state, ok := opaque.(*transitionAggregateState)
	if !ok || state == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid transition aggregate final state")
	}
	return state.value, nil
}

func AdmitAggregateSupport(c *Context, provider functions.AggregateProvider) error {
	p, ok := provider.(*TransitionAggregate)
	if !ok {
		return nil
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil || !auth.Live() {
		return err
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, p.Aggregate)
	if err != nil {
		return err
	}
	owner := auth
	owner.State.CurrentUser = current.Owner
	if err = owner.Roles.ValidatePrincipal(current.Owner); err != nil {
		return err
	}
	if err = CheckRecipeRoutineDescriptor(c, owner, dependencies, p.Support); err != nil {
		return err
	}
	if c.RoutineProgram != nil {
		return c.RoutineProgram.RecordActor(c, dependencies, p.Support, current.Owner, p.Aggregate)
	}
	return nil
}

func (p *TransitionAggregate) Remap(r functions.ProviderRemap) (functions.AggregateProvider, error) {
	if r.Routine == nil || r.Expression == nil {
		return nil, fmt.Errorf("stuntgres: missing transition aggregate remap authority")
	}
	if p.Aggregate == nil || p.Aggregate.Aggregate == nil || p.Support == nil || len(p.Inputs) == 0 || len(p.Inputs) != len(p.Formals) || len(p.Inputs) != len(p.Conversions) || p.Inputs[0] != p.Usage {
		return nil, fmt.Errorf("stuntgres: invalid transition frame declaration")
	}
	if err := validateTransitionCall(p.Call, p.Support, p.Usage); err != nil {
		return nil, err
	}
	next := *p
	var err error
	mapRoutine := func(old *catalog.Procedure) (*catalog.Procedure, error) {
		reference, err := r.Routine(old)
		if err != nil {
			return nil, err
		}
		next, ok := reference.(*catalog.Procedure)
		if !ok || next == nil {
			return nil, fmt.Errorf("stuntgres: invalid remapped transition declaration")
		}
		return next, nil
	}
	next.Aggregate, err = mapRoutine(p.Aggregate)
	if err != nil {
		return nil, err
	}
	next.Support, err = mapRoutine(p.Support)
	if err != nil {
		return nil, err
	}
	next.Usage, err = r.Usage(p.Usage)
	if err != nil {
		return nil, err
	}
	next.Inputs = make([]sqltype.Usage, len(p.Inputs))
	next.Formals = make([]sqltype.Usage, len(p.Formals))
	next.Conversions = make([]sqltype.CoercionPlan, len(p.Conversions))
	for i := range p.Inputs {
		next.Inputs[i], err = r.Usage(p.Inputs[i])
		if err != nil {
			return nil, err
		}
		next.Formals[i], err = r.Usage(p.Formals[i])
		if err != nil {
			return nil, err
		}
		next.Conversions[i], err = p.Conversions[i].Rebind(next.Inputs[i], next.Formals[i], sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	if p.InputCollation.ID != 0 {
		found := false
		for i, input := range p.Inputs {
			if input.Collation == p.InputCollation {
				next.InputCollation, found = next.Inputs[i].Collation, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("stuntgres: transition input collation has no typed owner")
		}
	}
	if next.Support.Strict && !next.Aggregate.Aggregate.Definition.HasInitial {
		next.SeedConversion, err = p.SeedConversion.Rebind(next.Inputs[1], next.Usage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	if p.Call != nil {
		next.Call, err = r.Expression(p.Call, p.Formals, next.Formals)
		if err != nil {
			return nil, err
		}
	}
	if err := validateTransitionCall(next.Call, next.Support, next.Usage); err != nil {
		return nil, err
	}
	return &next, nil
}

func validateTransitionCall(call expr.Expr, support *catalog.Procedure, result sqltype.Usage) error {
	if call == nil {
		if support.SQL == nil {
			return fmt.Errorf("stuntgres: missing transition scalar provider")
		}
		return nil
	}
	declaration, err := ExpressionRoutineDescriptor(call)
	if err != nil {
		return err
	}
	if declaration == nil || declaration.Address() != support.Address() || !SameRoutineDeclaration(declaration, support) || call.ResultUsage().Type != result.Type {
		return fmt.Errorf("stuntgres: mismatched transition scalar declaration")
	}
	return nil
}
