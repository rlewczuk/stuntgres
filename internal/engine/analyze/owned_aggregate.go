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
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindOwnedAggregate(raw parser.AggregateExpr, d *catalog.Procedure, name parser.ObjectName) (expr.Expr, error) {
	args := append([]expr.Expr(nil), b.Clause.SelectedOperatorArgs...)
	if len(args) != len(raw.Args) {
		return nil, fmt.Errorf("stuntgres: invalid selected aggregate arguments")
	}
	if len(args) == 0 && !raw.Star {
		return nil, expr.ErrorAt("42809", catalog.ObjectNameText(name)+"(*) must be used to call a parameterless aggregate function", raw.Pos)
	}
	filter, err := b.BindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	resultUsage := b.Type(sqltype.ID(d.Result)).Usage()
	info := b.Info(resultUsage, raw.Pos, expr.Properties{StageDependent: true})
	inputInfo := info
	// Only ordinary inputs determine the effective input collation. Each
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
	provider, err := b.ownedAggregateProvider(d, args, resultUsage, inputCollation, false, raw.Pos, inputInfo)
	if err != nil {
		return nil, err
	}
	ref := b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), d.Address())
	b.dependency(ref, d.Revision())
	call := plan.AggregateCall{Provider: provider, Routine: ref, Arguments: args, Operation: functions.AggregateTransition, IncludeNulls: true, Procedure: d, Ref: ref}
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		ns, ok := b.authorityNamespace(b.Query.Auth, d.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing aggregate namespace")
		}
		frozen := raw
		// Selected operands use binder-local slots; stored recipes retain the
		// immutable written argument vector, never those executable slots.
		frozen.Args = b.Clause.SelectedOperatorSyntax
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
func (b *Binder) ownedAggregateProvider(d *catalog.Procedure, args []expr.Expr, resultUsage sqltype.Usage, collation sqltype.CollationRef, moving bool, pos parser.Position, inputInfo expr.Info) (*exec.TransitionAggregate, error) {
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
		b.dependency(reference, support.Revision())
		formals, result, err := routine.AggregateSupportTypes(b.BindingCatalog(), b.DefinitionReader(), support, d.Aggregate, actual)
		if err != nil || result.Type != output.Type {
			return nil, nil, nil, nil, fmt.Errorf("stuntgres: invalid aggregate support signature")
		}
		conversions := make([]sqltype.CoercionPlan, len(actual))
		var parameters []expr.Expr
		if support.SQL == nil {
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
		if support.SQL == nil {
			call, err = b.BindSelectedOperatorRoutine(support, parameters, pos)
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
	if finalID != 0 {
		p.FinalInputs = inputs[:1]
		if extra {
			p.FinalInputs = inputs
		}
		p.Final, p.FinalFormals, p.FinalConversions, p.FinalCall, err = build(finalID, p.FinalInputs, resultUsage)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}
