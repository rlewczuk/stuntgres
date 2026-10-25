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
	Aggregate, Support        *catalog.Procedure
	Moving                    bool
	Initial                   string
	HasInitial                bool
	FinalModify               string
	InverseSupport            *catalog.Procedure
	InverseFormals            []sqltype.Usage
	InverseConversions        []sqltype.CoercionPlan
	InverseCall               expr.Expr
	StateUsage, Result        sqltype.Usage
	InputCollation            sqltype.CollationRef // Effective common ordinary input.
	Inputs, Formals           []sqltype.Usage      // state followed by fixed ordinary inputs
	Conversions               []sqltype.CoercionPlan
	SeedConversion            sqltype.CoercionPlan
	Call                      expr.Expr // Scalar provider over a private parameter frame.
	Final                     *catalog.Procedure
	FinalInputs, FinalFormals []sqltype.Usage
	FinalConversions          []sqltype.CoercionPlan
	FinalCall                 expr.Expr
}

type transitionAggregateState struct {
	owner        *TransitionAggregate
	value        sqltype.Value
	unseeded     bool
	frame        *proc.Frame
	finalFrame   *proc.Frame
	inverseFrame *proc.Frame
	contributed  uint64
}

func (p *TransitionAggregate) ResultUsage() sqltype.Usage { return p.Result }
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
	if !p.Support.Strict || p.HasInitial {
		return nil
	}
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	if _, compatible := catalog.ImplicitBinaryCast(auth.Catalog, p.Inputs[1].Type.ID(), p.StateUsage.Type.ID()); !compatible {
		return expr.ErrorAt("42P13", fmt.Sprintf("aggregate %d needs to have compatible input type and transition type", p.Aggregate.Address().ObjectID), parser.Position{})
	}
	return nil
}
func (p *TransitionAggregate) initialize(ctx context.Context, input sqltype.InputEnvironment) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &transitionAggregateState{owner: p, frame: proc.NewFrame(p.Formals)}
	if p.Final != nil {
		state.finalFrame = proc.NewFrame(p.FinalFormals)
	}
	if p.InverseSupport != nil {
		state.inverseFrame = proc.NewFrame(p.InverseFormals)
	}
	if err := p.reset(ctx, state, input); err != nil {
		return nil, err
	}
	return state, ctx.Err()
}

func (p *TransitionAggregate) reset(ctx context.Context, state *transitionAggregateState, input sqltype.InputEnvironment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value := sqltype.Null(p.StateUsage.Type)
	if p.HasInitial {
		var err error
		value, err = p.StateUsage.Type.InputWithEnvironment(ctx, p.Initial, p.StateUsage.Typmod, input)
		if err != nil {
			return err
		}
	}
	state.value, state.unseeded, state.contributed = value, !p.HasInitial, 0
	return ctx.Err()
}

func (p *TransitionAggregate) Advance(ctx context.Context, opaque any, values []sqltype.Value, evaluation expr.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, ok := opaque.(*transitionAggregateState)
	if !ok || state == nil || state.owner != p || len(values)+1 != len(p.Inputs) {
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
			if p.Moving {
				state.contributed++
			}
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
	for i := range p.Inputs {
		value := state.value
		if i > 0 {
			value = values[i-1]
		}
		converted, err := p.Conversions[i].Apply(ctx, value, runtime.Input, runtime.Output)
		if err != nil {
			return err
		}
		if err = state.frame.Bind(i, converted, p.Formals[i]); err != nil {
			return err
		}
	}
	result, err := p.invoke(ctx, runtime, p.Support, state.frame, p.StateUsage, p.Call)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if result.Type() != p.StateUsage.Type {
		return fmt.Errorf("stuntgres: invalid transition aggregate result")
	}
	if p.Moving {
		if result.IsNull() {
			return sqlError("22004", "moving-aggregate transition function must not return null")
		}
		state.contributed++
	}
	state.value, state.unseeded = result, false
	return nil
}

// Inverse returns true only when the selected inverse requested real frame
// replay. A NULL result never becomes the retained transition state.
func (p *TransitionAggregate) Inverse(ctx context.Context, opaque any, values []sqltype.Value, evaluation expr.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	state, ok := opaque.(*transitionAggregateState)
	if !p.Moving || p.InverseSupport == nil || !ok || state == nil || state.owner != p || len(values)+1 != len(p.Inputs) {
		return false, fmt.Errorf("stuntgres: invalid moving inverse state or arguments")
	}
	for i, value := range values {
		if value.Type() != p.Inputs[i+1].Type {
			return false, fmt.Errorf("stuntgres: foreign moving inverse input")
		}
		if p.InverseSupport.Strict && value.IsNull() {
			return false, ctx.Err()
		}
	}
	if state.contributed == 0 {
		return false, fmt.Errorf("stuntgres: moving inverse has no contributed row")
	}
	if state.contributed == 1 {
		return false, p.reset(ctx, state, evaluation.FunctionInput())
	}
	runtime, ok := evaluation.(*Context)
	if !ok {
		return false, fmt.Errorf("stuntgres: moving inverse has no statement runtime")
	}
	for i := range p.Inputs {
		value := state.value
		if i > 0 {
			value = values[i-1]
		}
		converted, err := p.InverseConversions[i].Apply(ctx, value, runtime.Input, runtime.Output)
		if err != nil {
			return false, err
		}
		if err = state.inverseFrame.Bind(i, converted, p.InverseFormals[i]); err != nil {
			return false, err
		}
	}
	result, err := p.invoke(ctx, runtime, p.InverseSupport, state.inverseFrame, p.StateUsage, p.InverseCall)
	if err != nil {
		return false, err
	}
	if result.Type() != p.StateUsage.Type {
		return false, fmt.Errorf("stuntgres: invalid moving inverse result")
	}
	if result.IsNull() {
		return true, ctx.Err()
	}
	state.value = result
	state.contributed--
	return false, ctx.Err()
}
func (p *TransitionAggregate) Finalize(ctx context.Context, opaque any) (sqltype.Value, error) {
	if p.Final != nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate final function requires execution context")
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	state, ok := opaque.(*transitionAggregateState)
	if !ok || state == nil || state.owner != p {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid transition aggregate final state")
	}
	return state.value, nil
}

// FinalizeWithContext never replaces the ending transition state. Value carriers
// are immutable; every group owns both frames, including for READ_WRITE finals.
func (p *TransitionAggregate) FinalizeWithContext(ctx context.Context, opaque any, evaluation expr.Context) (sqltype.Value, error) {
	if p.Final == nil {
		return p.Finalize(ctx, opaque)
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	state, ok := opaque.(*transitionAggregateState)
	if !ok || state == nil || state.owner != p || state.finalFrame == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid aggregate final state")
	}
	runtime, ok := evaluation.(*Context)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate final has no statement runtime")
	}
	for i, input := range p.FinalInputs {
		value := sqltype.Null(input.Type)
		if i == 0 {
			value = state.value
		}
		converted, err := p.FinalConversions[i].Apply(ctx, value, runtime.Input, runtime.Output)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err = state.finalFrame.Bind(i, converted, p.FinalFormals[i]); err != nil {
			return sqltype.Value{}, err
		}
		if p.Final.Strict && converted.IsNull() {
			return sqltype.Null(p.Result.Type), ctx.Err()
		}
	}
	result, err := p.invoke(ctx, runtime, p.Final, state.finalFrame, p.Result, p.FinalCall)
	if err != nil {
		return sqltype.Value{}, err
	}
	if result.Type() != p.Result.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid aggregate final result")
	}
	return result, nil
}

func (p *TransitionAggregate) invoke(ctx context.Context, runtime *Context, support *catalog.Procedure, frame *proc.Frame, usage sqltype.Usage, call expr.Expr) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var result sqltype.Value
	var err error
	if support.SQL != nil {
		host, ok := runtime.LiveHost().(interface {
			InvokeAggregateSQLRoutine(*catalog.Procedure, *proc.Frame, sqltype.Usage, *catalog.Procedure, sqltype.CollationRef) (sqltype.Value, error)
		})
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate support invocation has no statement owner")
		}
		result, err = host.InvokeAggregateSQLRoutine(support, frame, usage, p.Aggregate, p.InputCollation)
	} else {
		local := *runtime
		local.Parameters = frame.Parameters
		result, err = call.Eval(&local)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if err = ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return result, nil
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
	for _, support := range []*catalog.Procedure{p.Support, p.InverseSupport, p.Final} {
		if support == nil {
			continue
		}
		if err = CheckRecipeRoutineDescriptor(c, owner, dependencies, support); err != nil {
			return err
		}
		if c.RoutineProgram != nil {
			if err = c.RoutineProgram.RecordActor(c, dependencies, support, current.Owner, p.Aggregate); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *TransitionAggregate) Remap(r functions.ProviderRemap) (functions.AggregateProvider, error) {
	if r.Routine == nil || r.Expression == nil || r.Usage == nil {
		return nil, fmt.Errorf("stuntgres: missing transition aggregate remap authority")
	}
	if p.Aggregate == nil || p.Aggregate.Aggregate == nil || p.Support == nil || len(p.Inputs) == 0 || len(p.Inputs) != len(p.Formals) || len(p.Inputs) != len(p.Conversions) || p.Inputs[0] != p.StateUsage {
		return nil, fmt.Errorf("stuntgres: invalid transition frame declaration")
	}
	if err := validateAggregateConversions(p.Inputs, p.Formals, p.Conversions, p.Aggregate.Aggregate.BinaryRecipe); err != nil {
		return nil, err
	}
	if err := validateTransitionCall(p.Call, p.Support, p.StateUsage); err != nil {
		return nil, err
	}
	if err := p.ValidateSelection(); err != nil {
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
	next.StateUsage, err = r.Usage(p.StateUsage)
	if err != nil {
		return nil, err
	}
	next.Result, err = r.Usage(p.Result)
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
	if next.Support.Strict && !next.HasInitial {
		next.SeedConversion, err = p.SeedConversion.Rebind(next.Inputs[1], next.StateUsage, sqltype.Implicit)
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
	if err := validateTransitionCall(next.Call, next.Support, next.StateUsage); err != nil {
		return nil, err
	}
	if err := validateAggregateConversions(next.Inputs, next.Formals, next.Conversions, next.Aggregate.Aggregate.BinaryRecipe); err != nil {
		return nil, err
	}
	if p.Final != nil {
		next.Final, err = mapRoutine(p.Final)
		if err != nil {
			return nil, err
		}
		next.FinalInputs = make([]sqltype.Usage, len(p.FinalInputs))
		next.FinalFormals = make([]sqltype.Usage, len(p.FinalFormals))
		next.FinalConversions = make([]sqltype.CoercionPlan, len(p.FinalConversions))
		for i := range p.FinalInputs {
			next.FinalInputs[i], err = r.Usage(p.FinalInputs[i])
			if err != nil {
				return nil, err
			}
			next.FinalFormals[i], err = r.Usage(p.FinalFormals[i])
			if err != nil {
				return nil, err
			}
			next.FinalConversions[i], err = p.FinalConversions[i].Rebind(next.FinalInputs[i], next.FinalFormals[i], sqltype.Implicit)
			if err != nil {
				return nil, err
			}
		}
		if p.FinalCall != nil {
			next.FinalCall, err = r.Expression(p.FinalCall, p.FinalFormals, next.FinalFormals)
			if err != nil {
				return nil, err
			}
		}
	}
	if p.InverseSupport != nil {
		next.InverseSupport, err = mapRoutine(p.InverseSupport)
		if err != nil {
			return nil, err
		}
		next.InverseFormals = make([]sqltype.Usage, len(p.InverseFormals))
		next.InverseConversions = make([]sqltype.CoercionPlan, len(p.InverseConversions))
		for i, formal := range p.InverseFormals {
			next.InverseFormals[i], err = r.Usage(formal)
			if err != nil {
				return nil, err
			}
			next.InverseConversions[i], err = p.InverseConversions[i].Rebind(next.Inputs[i], next.InverseFormals[i], sqltype.Implicit)
			if err != nil {
				return nil, err
			}
		}
		if p.InverseCall != nil {
			next.InverseCall, err = r.Expression(p.InverseCall, p.InverseFormals, next.InverseFormals)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := next.ValidateSelection(); err != nil {
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

func (p *TransitionAggregate) validateFinalFrame() error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid aggregate final frame declaration") }
	definition := &p.Aggregate.Aggregate.Definition
	finalSlot, extra := 1, definition.FinalExtra
	if p.Moving {
		finalSlot, extra = 7, definition.MovingFinalExtra
	}
	if p.Final == nil {
		if definition.Functions[finalSlot] != 0 || p.FinalCall != nil || len(p.FinalInputs) != 0 || len(p.FinalFormals) != 0 || len(p.FinalConversions) != 0 || p.Result != p.StateUsage {
			return invalid()
		}
		return nil
	}
	count := 1
	if extra {
		count = len(p.Inputs)
	}
	if p.Final.Address().ObjectID != definition.Functions[finalSlot] || len(p.FinalInputs) != count || len(p.FinalFormals) != count || len(p.FinalConversions) != count {
		return invalid()
	}
	for i, input := range p.FinalInputs {
		if input != p.Inputs[i] {
			return invalid()
		}
	}
	if err := validateAggregateConversions(p.FinalInputs, p.FinalFormals, p.FinalConversions, p.Aggregate.Aggregate.BinaryRecipe); err != nil {
		return err
	}
	return validateTransitionCall(p.FinalCall, p.Final, p.Result)
}

func validateAggregateConversions(inputs, formals []sqltype.Usage, conversions []sqltype.CoercionPlan, recipe *catalog.AggregateBinaryRecipe) error {
	if len(inputs) != len(formals) || len(inputs) != len(conversions) {
		return fmt.Errorf("stuntgres: mismatched aggregate conversion vector")
	}
	for i, input := range inputs {
		if input.Type == nil || !input.Valid() || formals[i].Type == nil || !formals[i].Valid() {
			return fmt.Errorf("stuntgres: invalid aggregate conversion usage")
		}
		expected, err := catalog.AggregateConversion(recipe, input, formals[i])
		if err != nil || !conversions[i].Equal(expected) {
			return fmt.Errorf("stuntgres: mismatched retained aggregate conversion")
		}
	}
	return nil
}

// ValidateSelection checks detached provider identity and the exact selected
// implementation, before and after remapping. Catalog specialization is checked
// separately by stored frame validation.
func (p *TransitionAggregate) ValidateSelection() error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid selected aggregate implementation") }
	if p.Aggregate == nil || p.Aggregate.Aggregate == nil || p.Support == nil || p.StateUsage.Type == nil || !p.StateUsage.Valid() || p.Result.Type == nil || !p.Result.Valid() || p.Result.Type.ID() != sqltype.ID(p.Aggregate.Result) {
		return invalid()
	}
	d := &p.Aggregate.Aggregate.Definition
	slot, state, initial, hasInitial, mode := 0, d.TransitionType, d.Initial, d.HasInitial, d.FinalModify
	if p.Moving {
		slot, state, initial, hasInitial, mode = 5, d.MovingType, d.MovingInitial, d.HasMovingInitial, d.MovingFinalModify
	}
	if p.Support.Address().ObjectID != d.Functions[slot] || state == 0 || p.StateUsage.Type.ID() != sqltype.ID(state) || p.Initial != initial || p.HasInitial != hasInitial || p.FinalModify != mode {
		return invalid()
	}
	if p.Support.Strict && !p.HasInitial {
		if len(p.Inputs) < 2 {
			return invalid()
		}
		expected, err := catalog.AggregateConversion(p.Aggregate.Aggregate.BinaryRecipe, p.Inputs[1], p.StateUsage)
		if err != nil || !p.SeedConversion.Equal(expected) {
			return invalid()
		}
	}
	if p.Moving {
		if p.InverseSupport == nil || p.InverseSupport.Address().ObjectID != d.Functions[6] || p.InverseSupport.Strict != p.Support.Strict {
			return invalid()
		}
		if err := validateAggregateConversions(p.Inputs, p.InverseFormals, p.InverseConversions, p.Aggregate.Aggregate.BinaryRecipe); err != nil {
			return err
		}
		if err := validateTransitionCall(p.InverseCall, p.InverseSupport, p.StateUsage); err != nil {
			return err
		}
	} else if p.InverseSupport != nil || p.InverseCall != nil || len(p.InverseFormals) != 0 || len(p.InverseConversions) != 0 {
		return invalid()
	}
	return p.validateFinalFrame()
}
