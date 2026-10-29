package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindOwnedAggregate(raw parser.AggregateExpr, d *catalog.Procedure, name parser.ObjectName) (expr.Expr, error) {
	args := append([]expr.Expr(nil), b.Clause.SelectedOperatorArgs...)
	definition := &d.Aggregate.Definition
	ordered := definition.Kind != "n"
	directCount := 0
	if raw.Within {
		directCount = len(raw.Args)
	}
	expectedCount := len(raw.Args)
	if raw.Within {
		expectedCount += len(raw.Order)
	}
	if len(args) != expectedCount {
		return nil, fmt.Errorf("stuntgres: invalid selected aggregate arguments")
	}
	if ordered && !raw.Within {
		return nil, expr.ErrorAt("42809", "WITHIN GROUP is required for ordered-set aggregate "+catalog.ObjectNameText(name), raw.Pos)
	}
	if !ordered && raw.Within {
		return nil, expr.ErrorAt("42809", catalog.ObjectNameText(name)+" is not an ordered-set aggregate, so it cannot have WITHIN GROUP", raw.Pos)
	}
	if ordered && raw.Window != nil {
		return nil, expr.ErrorAt("0A000", "OVER is not supported for ordered-set aggregate "+catalog.ObjectNameText(name), raw.Pos)
	}
	if ordered && !OwnedAggregateDirectCountValid(d, directCount, len(raw.Order)) {
		return nil, b.ownedAggregateDirectCountError(raw, d, name, args)
	}
	if !ordered && len(args) == 0 && !raw.Star {
		return nil, expr.ErrorAt("42809", catalog.ObjectNameText(name)+"(*) must be used to call a parameterless aggregate function", raw.Pos)
	}
	filter, err := b.BindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	call := plan.AggregateCall{Arguments: args[directCount:], Direct: args[:directCount], Operation: functions.AggregateTransition, IncludeNulls: true, Procedure: d}
	if ordered {
		call.StageWidth = len(call.Arguments)
		call.Order = make([]plan.SortKey, len(raw.Order))
		// Resolve every ordering capability before checking any pair's
		// collation or deriving the support routine's common input collation.
		for i, key := range raw.Order {
			argument := call.Arguments[i]
			comparison, e := functions.AggregateOrdering(argument.ResultUsage(), key.Pos, false)
			if e != nil {
				return nil, e
			}
			call.Order[i] = plan.SortKey{Expression: argument, Comparison: comparison, Position: key.Pos.Character, Desc: key.Desc, NullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc, Slot: i, Cache: i}
		}
		if definition.Kind == "h" {
			prefix := len(call.Direct) - len(call.Arguments)
			for i, argument := range call.Arguments {
				pairInfo, e := b.ComparisonInfo(call.Direct[prefix+i], argument)
				if e != nil {
					return nil, e
				}
				if pairInfo.CollationConflict {
					diagnostic := expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(pairInfo.CollationLeft.ID), b.collationName(pairInfo.CollationRight.ID)), pairInfo.CollationConflictPos).(*sqlstate.Error)
					diagnostic.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
					return nil, diagnostic
				}
				usage := argument.ResultUsage()
				usage.Collation = pairInfo.Usage.Collation
				call.Order[i].Comparison, e = functions.AggregateOrdering(usage, raw.Order[i].Pos, false)
				if e != nil {
					return nil, e
				}
			}
		}
	}
	resultUsage := b.Type(sqltype.ID(d.Result)).Usage()
	info := b.Info(resultUsage, raw.Pos, expr.Properties{StageDependent: true})
	inputInfo := info
	// Direct and row inputs determine the effective support collation. Each
	// collatable state/result retains its own fallback when no input collates.
	collatableInput := false
	for _, arg := range args {
		if arg.ResultUsage().Collation.ID != 0 {
			collatableInput = true
			inputInfo.Usage = arg.ResultUsage()
			break
		}
	}
	inputInfo, err = b.CollatedInfo(inputInfo, args...)
	if err != nil {
		return nil, err
	}
	if collatableInput {
		if resultUsage.Collation.ID != 0 {
			info = inputInfo
			info.Usage = resultUsage
			info.Usage.Collation = inputInfo.Usage.Collation
			resultUsage = info.Usage
		}
	}
	inputCollation := sqltype.CollationRef{}
	if collatableInput && !inputInfo.CollationConflict {
		inputCollation = inputInfo.Usage.Collation
	}
	var provider *exec.TransitionAggregate
	if raw.Window == nil {
		provider, err = b.ownedAggregateProvider(d, args[directCount:], args[:directCount], resultUsage, inputCollation, false, raw.Pos, inputInfo)
		if err != nil {
			return nil, err
		}
	}
	ref := b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), d.Address())
	b.dependency(ref, d.Revision())
	call.Provider, call.Routine, call.Ref = provider, ref, ref
	if ordered && provider.StateUsage.Type.ID() == 2281 {
		provider.ContextOrder = call.Order
		keys := make([]functions.AggregateSortKey, len(call.Order))
		for i, key := range call.Order {
			keys[i] = functions.AggregateSortKey{Expression: key.Expression, Comparison: key.Comparison, Key: raw.Order[i], Argument: true}
		}
		provider.ContextSupport, err = functions.NewAggregateContextSupport(uint32(provider.Support.Address().ObjectID), uint32(definition.Functions[1]), definition.FinalExtra, definition.Kind == "h", provider.Result, provider.Inputs[1:], provider.DirectInputs, keys)
		if err != nil {
			return nil, err
		}
	}

	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		ns, ok := b.authorityNamespace(b.Query.Auth, d.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing aggregate namespace")
		}
		frozen := raw
		// Selected operands use binder-local slots; stored recipes retain the
		// immutable written argument vector, never those executable slots.
		frozen.Args = b.Clause.SelectedOperatorSyntax[:directCount]
		if !raw.Within {
			frozen.Args = b.Clause.SelectedOperatorSyntax
		}
		if raw.Within {
			frozen.Order = append([]parser.OrderKey(nil), raw.Order...)
			for i := range frozen.Order {
				frozen.Order[i].Expr = b.Clause.SelectedOperatorSyntax[directCount+i]
			}
		}
		frozen.StoredAggregate = true
		frozen.Schema = &parser.Identifier{Text: ns.Name()}
		if b.Stored.Syntax == nil {
			b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
		}
		// The source function and its selected aggregate are the same written
		// occurrence. Binder-local input slots must not change its syntax key.
		b.Stored.Syntax[b.storedExpressionSyntaxKey(frozen)] = frozen
	}
	if raw.Window != nil {
		return b.bindWindowCall(raw, name, &call, info)
	}
	return b.finishAggregate(raw, call, filter, info)
}

// Build a detached selected implementation from already checked ordinary
// operands. Window selection never mutates an ordinary/held provider.
func (b *Binder) ownedAggregateProvider(d *catalog.Procedure, args, direct []expr.Expr, resultUsage sqltype.Usage, collation sqltype.CollationRef, moving bool, pos parser.Position, inputInfo expr.Info) (*exec.TransitionAggregate, error) {
	definition := &d.Aggregate.Definition
	stateID, forwardID, finalID := definition.TransitionType, definition.Functions[0], definition.Functions[1]
	seed, hasSeed, extra, mode := definition.Initial, definition.HasInitial, definition.FinalExtra, definition.FinalModify
	if moving {
		stateID, forwardID, finalID = definition.MovingType, definition.Functions[5], definition.Functions[7]
		seed, hasSeed, extra, mode = definition.MovingInitial, definition.HasMovingInitial, definition.MovingFinalExtra, definition.MovingFinalModify
	}
	state := b.Type(sqltype.ID(stateID)).Usage()
	if collation.ID != 0 && state.Collation.ID != 0 {
		state.Collation = collation
	}
	inputs := make([]sqltype.Usage, len(args)+1)
	inputs[0] = state
	for i, arg := range args {
		inputs[i+1] = arg.ResultUsage()
		if collation.ID != 0 && inputs[i+1].Collation.ID != 0 {
			inputs[i+1].Collation = collation
		}
	}
	build := func(oid ids.OID, actual []sqltype.Usage, output sqltype.Usage) (*catalog.Procedure, []sqltype.Usage, []sqltype.CoercionPlan, expr.Expr, error) {
		descriptor, found := b.definitionDescriptor(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		support, valid := descriptor.(*catalog.Procedure)
		if !found || !valid {
			return nil, nil, nil, nil, fmt.Errorf("stuntgres: missing aggregate support routine")
		}
		reference := b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), support.Address())
		b.aggregateSupportDependency(reference, support.Revision())
		formals, result, err := routine.AggregateSupportTypes(b.BindingCatalog(), b.DefinitionReader(), support, d.Aggregate, actual)
		if err != nil || result.Type != output.Type {
			return nil, nil, nil, nil, fmt.Errorf("stuntgres: invalid aggregate support signature")
		}
		if functions.IsAggregateContextSupport(uint32(oid)) {
			return support, append([]sqltype.Usage(nil), actual...), nil, nil, nil
		}
		conversions := make([]sqltype.CoercionPlan, len(actual))
		var parameters []expr.Expr
		if support.RoutineDefinition() == nil {
			parameters = make([]expr.Expr, len(actual))
		}
		for i, input := range actual {
			conversions[i], err = catalog.AggregateConversion(d.Aggregate.BinaryRecipe, input, formals[i])
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if parameters != nil {
				parameterInfo := expr.Info{Usage: formals[i], Pos: pos, Property: expr.Properties{ParameterDependent: true}}
				if formals[i].Collation.ID != 0 {
					parameterInfo = inputInfo
					parameterInfo.Usage = formals[i]
					parameterInfo.Property = expr.Properties{ParameterDependent: true}
				}
				parameters[i] = plan.ParameterExpr{Info: parameterInfo, Number: i + 1}
			}
		}
		var call expr.Expr
		if support.RoutineDefinition() == nil {
			call, err = b.bindAggregateSupport(support, parameters, pos)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if call.ResultUsage().Type != output.Type {
				return nil, nil, nil, nil, fmt.Errorf("stuntgres: invalid aggregate scalar result")
			}
		}
		return support, formals, conversions, call, nil
	}
	p := &exec.TransitionAggregate{Aggregate: d, Moving: moving, Initial: seed, HasInitial: hasSeed, FinalModify: mode, StateUsage: state, Result: resultUsage, Inputs: inputs, InputCollation: collation}
	p.DirectInputs = make([]sqltype.Usage, len(direct))
	for i, argument := range direct {
		p.DirectInputs[i] = argument.ResultUsage()
	}
	var err error
	p.Support, p.Formals, p.Conversions, p.Call, err = build(forwardID, inputs, state)
	if err != nil {
		return nil, err
	}
	if p.Support.Strict && !hasSeed {
		if len(inputs) == 1 {
			return nil, fmt.Errorf("stuntgres: strict aggregate has no seed input")
		}
		p.SeedConversion, err = catalog.AggregateConversion(d.Aggregate.BinaryRecipe, inputs[1], state)
		if err != nil {
			return nil, err
		}
	}
	if moving {
		p.InverseSupport, p.InverseFormals, p.InverseConversions, p.InverseCall, err = build(definition.Functions[6], inputs, state)
		if err != nil {
			return nil, err
		}
	}
	if !moving && definition.Functions[2] != 0 && state.Type.Concrete() {
		actual := []sqltype.Usage{state, state}
		combine := &exec.TransitionAggregate{Aggregate: d, Combining: true, Initial: seed, HasInitial: hasSeed, FinalModify: mode, StateUsage: state, Result: state, Inputs: actual, InputCollation: collation}
		combine.Support, combine.Formals, combine.Conversions, combine.Call, err = build(definition.Functions[2], actual, state)
		if err != nil {
			return nil, err
		}
		if combine.Support.Strict && !hasSeed {
			combine.SeedConversion, err = catalog.AggregateConversion(d.Aggregate.BinaryRecipe, state, state)
			if err != nil {
				return nil, err
			}
		}
		p.CombinePlan = combine
	}
	if finalID != 0 {
		p.FinalInputs = make([]sqltype.Usage, 1, 1+len(direct)+len(args))
		p.FinalInputs[0] = state
		p.FinalInputs = append(p.FinalInputs, p.DirectInputs...)
		if extra {
			p.FinalInputs = append(p.FinalInputs, inputs[1:]...)
		}
		p.Final, p.FinalFormals, p.FinalConversions, p.FinalCall, err = build(finalID, p.FinalInputs, resultUsage)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (b *Binder) aggregateSupportDependency(ref ids.ObjectRef, revision ids.DefinitionRevision) {
	previous := b.dependencyPurpose
	b.dependencyPurpose = catalog.AggregateSupportDependency
	defer func() { b.dependencyPurpose = previous }()
	b.dependency(ref, revision)
}

// Synthetic provider binding can itself capture the selected routine and types.
// Scope only that binding, never the aggregate's written operands or FILTER.
func (b *Binder) bindAggregateSupport(d *catalog.Procedure, args []expr.Expr, pos parser.Position) (expr.Expr, error) {
	previous := b.dependencyPurpose
	b.dependencyPurpose = catalog.AggregateSupportDependency
	defer func() { b.dependencyPurpose = previous }()
	return b.BindSelectedOperatorRoutine(d, args, pos)
}
