package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
	DirectInputs              []sqltype.Usage
	ContextSupport            *functions.AggregateContextSupport
	ContextOrder              []plan.SortKey
	Conversions               []sqltype.CoercionPlan
	SeedConversion            sqltype.CoercionPlan
	Call                      expr.Expr // Scalar provider over a private parameter frame.
	Final                     *catalog.Procedure
	FinalInputs, FinalFormals []sqltype.Usage
	FinalConversions          []sqltype.CoercionPlan
	FinalCall                 expr.Expr
	CombinePlan               *TransitionAggregate // Closed state,state frame, without a final implementation.
	Combining                 bool
}

type transitionAggregateState struct {
	owner        *TransitionAggregate
	value        sqltype.Value
	unseeded     bool
	frame        *proc.Frame
	finalFrame   *proc.Frame
	inverseFrame *proc.Frame
	contributed  uint64
	contextState any
	combineFrame *proc.Frame
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
	state := &transitionAggregateState{owner: p}
	if p.ContextSupport != nil {
		state.contextState = p.ContextSupport.NewState()
		return state, ctx.Err()
	}
	state.frame = proc.NewFrame(p.Formals)
	if p.Final != nil {
		state.finalFrame = proc.NewFrame(p.FinalFormals)
	}
	if p.InverseSupport != nil {
		state.inverseFrame = proc.NewFrame(p.InverseFormals)
	}
	if p.CombinePlan != nil {
		state.combineFrame = proc.NewFrame(p.CombinePlan.Formals)
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
	if p.ContextSupport != nil {
		return p.ContextSupport.Advance(ctx, state.contextState, values)
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
func (p *TransitionAggregate) FinalizeWithContext(ctx context.Context, opaque any, direct []sqltype.Value, evaluation expr.Context) (sqltype.Value, error) {
	if len(direct) != len(p.DirectInputs) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid aggregate direct arguments")
	}
	for i, value := range direct {
		if value.Type() != p.DirectInputs[i].Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign aggregate direct argument")
		}
	}
	if p.ContextSupport != nil {
		state, ok := opaque.(*transitionAggregateState)
		if !ok || state == nil || state.owner != p {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid aggregate-context state")
		}
		return p.ContextSupport.Finalize(ctx, state.contextState, direct, evaluation.FunctionInput(), functions.AggregateContextOrdering{
			Compare: func(ctx context.Context, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error) {
				return CompareOrderedAggregateTuple(ctx, p.ContextOrder, left, right, env)
			},
			Sort: MergeQueryIndices,
		})
	}
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
		} else if i <= len(direct) {
			value = direct[i-1]
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
			InvokeAggregateSQLRoutine(context.Context, *catalog.Procedure, *proc.Frame, sqltype.Usage, *catalog.Procedure, sqltype.CollationRef) (sqltype.Value, error)
		})
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate support invocation has no statement owner")
		}
		result, err = host.InvokeAggregateSQLRoutine(ctx, support, frame, usage, p.Aggregate, p.InputCollation)
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

func AdmitAggregateSupport(c *Context, call *plan.AggregateCall) error {
	if p, ok := call.Provider.(*TransitionAggregate); ok {
		// Final support admission precedes transition initialization, including
		// strict finals and empty groups that will never invoke the function.
		return admitAggregateComponents(c, p.Aggregate, p.Final, p.Support, p.InverseSupport)
	}
	if call.Operation == functions.AggregateString {
		return admitStringAggregateCall(c, call, 1<<0|1<<1)
	}
	return nil
}

func AdmitAggregatePartialSupport(c *Context, call *plan.AggregateCall, worker bool) error {
	if p, ok := call.Provider.(*TransitionAggregate); ok {
		if worker {
			return admitAggregateComponents(c, p.Aggregate, p.Support)
		}
		if p.CombinePlan == nil {
			return fmt.Errorf("stuntgres: missing aggregate combine implementation")
		}
		return admitAggregateComponents(c, p.Aggregate, p.Final, p.CombinePlan.Support)
	}
	if call.Operation == functions.AggregateString {
		if worker {
			return admitStringAggregateCall(c, call, 1<<0|1<<3)
		}
		return admitStringAggregateCall(c, call, 1<<1|1<<2|1<<4)
	}
	return nil
}

func admitStringAggregateCall(c *Context, call *plan.AggregateCall, active uint8) error {
	local := *c
	if call.RoutineRecipe != nil && (!local.RoutineGraft || local.RoutineGraftSource != call.RoutineRecipe) || call.RoutineRecipe == nil && local.RoutineGraft {
		if err := SelectExpressionRecipe(&local, call.RoutineRecipe); err != nil {
			return err
		}
	}
	aggregate := call.Procedure
	if aggregate == nil || aggregate.Builtin == nil {
		return fmt.Errorf("stuntgres: missing string aggregate owning declaration")
	}
	definition := catalog.StringAggregateDefinition(aggregate.Builtin.Identity.ID)
	if definition.Function == 0 {
		return fmt.Errorf("stuntgres: invalid string aggregate owning declaration")
	}
	var components [5]*catalog.Procedure
	count := 0
	// Final/transport support admission precedes transition initialization.
	for _, slot := range [5]int{1, 3, 4, 0, 2} {
		if active&(1<<slot) == 0 {
			continue
		}
		support := call.PrimaryStringSupport[slot]
		if support == nil || support.Builtin == nil || support.Address().ClassID != ids.ProcedureClassID || support.Address().ObjectID != definition.Functions[slot] || support.Builtin.Identity.ID != definition.Functions[slot] {
			return fmt.Errorf("stuntgres: mismatched string aggregate support admission")
		}
		components[count] = support
		count++
	}
	return admitAggregateComponents(&local, aggregate, components[:count]...)
}

func admitAggregateComponents(c *Context, aggregate *catalog.Procedure, components ...*catalog.Procedure) error {
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil || !auth.Live() {
		return err
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, aggregate)
	if err != nil {
		return err
	}
	owner := auth
	owner.State.CurrentUser = current.Owner
	if err = owner.Roles.ValidatePrincipal(current.Owner); err != nil {
		return err
	}
	for _, support := range components {
		if support == nil {
			continue
		}
		if err = checkRecipeRoutineDescriptor(c, owner, dependencies, support, catalog.AggregateSupportDependency); err != nil {
			return err
		}
		if c.RoutineProgram != nil {
			if err = c.RoutineProgram.RecordActor(c, dependencies, support, current.Owner, aggregate); err != nil {
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
	if p.ContextSupport != nil {
		return p.remapContextSupport(r)
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
	next.DirectInputs = make([]sqltype.Usage, len(p.DirectInputs))
	for i, input := range p.DirectInputs {
		next.DirectInputs[i], err = r.Usage(input)
		if err != nil {
			return nil, err
		}
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
			for i, input := range p.DirectInputs {
				if input.Collation == p.InputCollation {
					next.InputCollation, found = next.DirectInputs[i].Collation, true
					break
				}
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
	if p.CombinePlan != nil {
		mapped, err := p.CombinePlan.Remap(r)
		if err != nil {
			return nil, err
		}
		next.CombinePlan = mapped.(*TransitionAggregate)
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
	count := 1 + len(p.DirectInputs)
	if extra {
		count += len(p.Inputs) - 1
	}
	if p.Final.Address().ObjectID != definition.Functions[finalSlot] || len(p.FinalInputs) != count || len(p.FinalFormals) != count || len(p.FinalConversions) != count {
		return invalid()
	}
	for i, input := range p.FinalInputs {
		expected := p.StateUsage
		if i > 0 && i <= len(p.DirectInputs) {
			expected = p.DirectInputs[i-1]
		}
		if i > len(p.DirectInputs) {
			expected = p.Inputs[i-len(p.DirectInputs)]
		}
		if input != expected {
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
	if p.Aggregate == nil || p.Aggregate.Aggregate == nil || p.Support == nil || p.StateUsage.Type == nil || (!p.StateUsage.Valid() && !(p.ContextSupport != nil && p.StateUsage.Type.ID() == 2281 && p.StateUsage == p.StateUsage.Type.Usage())) || p.Result.Type == nil || !p.Result.Valid() || !p.Combining && p.Result.Type.ID() != sqltype.ID(p.Aggregate.Result) {
		return invalid()
	}
	d := &p.Aggregate.Aggregate.Definition
	slot, state, initial, hasInitial, mode := 0, d.TransitionType, d.Initial, d.HasInitial, d.FinalModify
	if p.Moving {
		slot, state, initial, hasInitial, mode = 5, d.MovingType, d.MovingInitial, d.HasMovingInitial, d.MovingFinalModify
	}
	if p.Combining {
		slot = 2
		if p.Moving || p.CombinePlan != nil || p.Final != nil || p.Result != p.StateUsage || len(p.DirectInputs) != 0 || len(p.Inputs) != 2 || p.Inputs[0] != p.StateUsage || p.Inputs[1] != p.StateUsage {
			return invalid()
		}
	}
	if p.Support.Address().ObjectID != d.Functions[slot] || state == 0 || p.StateUsage.Type.ID() != sqltype.ID(state) || p.Initial != initial || p.HasInitial != hasInitial || p.FinalModify != mode {
		return invalid()
	}
	if p.ContextSupport != nil {
		if p.Moving || p.Final == nil || uint32(p.Support.Address().ObjectID) != p.ContextSupport.TransitionOID() || uint32(p.Final.Address().ObjectID) != p.ContextSupport.FinalOID() || p.ContextSupport.ResultUsage() != p.Result || p.ContextSupport.FinalExtra() != d.FinalExtra || p.ContextSupport.Hypothetical() != (d.Kind == "h") || p.StateUsage.Type.ID() != 2281 || len(p.Conversions) != 0 || p.Call != nil || p.FinalCall != nil {
			return invalid()
		}
		return p.validateContextSelection()
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
	if p.Combining {
		return nil
	}
	if p.CombinePlan != nil {
		if !p.CombinePlan.Combining || p.CombinePlan.Aggregate != p.Aggregate || p.CombinePlan.StateUsage != p.StateUsage {
			return invalid()
		}
		if err := p.CombinePlan.ValidateSelection(); err != nil {
			return err
		}
	}
	return p.validateFinalFrame()
}
func (p *TransitionAggregate) PartialCapable() bool {
	// Domain INITCOND/CHECK callbacks borrow mutable statement input machinery.
	// They retain ordinary serial semantics rather than entering that host from
	// independent worker initializers outside the SQL-support invocation gate.
	return p.CombinePlan != nil && !p.Moving && p.ContextSupport == nil && p.StateUsage.Type != nil && !p.StateUsage.Type.IsDomain()
}

func (p *TransitionAggregate) Combine(ctx context.Context, destination, incoming any, evaluation expr.Context) error {
	left, lok := destination.(*transitionAggregateState)
	right, rok := incoming.(*transitionAggregateState)
	if !lok || !rok || left == nil || right == nil || left.owner != p || right.owner != p || p.CombinePlan == nil || left.combineFrame == nil {
		return fmt.Errorf("stuntgres: invalid aggregate combine state")
	}
	local := transitionAggregateState{owner: p.CombinePlan, value: left.value, unseeded: left.unseeded, frame: left.combineFrame}
	values := [1]sqltype.Value{right.value}
	if err := p.CombinePlan.Advance(ctx, &local, values[:], evaluation); err != nil {
		return err
	}
	left.value, left.unseeded = local.value, local.unseeded
	return nil
}
