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
	state := b.Type(sqltype.ID(d.Aggregate.Definition.TransitionType)).Usage()
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
		if state.Collation.ID != 0 {
			state.Collation = inputInfo.Usage.Collation
		}
		if resultUsage.Collation.ID != 0 {
			info = inputInfo
			info.Usage = resultUsage
			info.Usage.Collation = inputInfo.Usage.Collation
			resultUsage = info.Usage
		}
	}
	supportDescriptor, ok := b.definitionDescriptor(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Aggregate.Definition.Functions[0]})
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing aggregate support routine")
	}
	support, ok := supportDescriptor.(*catalog.Procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid aggregate support routine")
	}
	ref := b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), d.Address())
	supportRef := b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), support.Address())
	b.dependency(ref, d.Revision())
	b.dependency(supportRef, support.Revision())
	inputs := make([]sqltype.Usage, len(args)+1)
	inputs[0] = state
	for i, arg := range args {
		inputs[i+1] = arg.ResultUsage()
		if inputs[i+1].Collation.ID != 0 {
			inputs[i+1].Collation = inputInfo.Usage.Collation
		}
	}
	formals, result, err := routine.AggregateSupportTypes(b.BindingCatalog(), b.DefinitionReader(), support, d.Aggregate, inputs)
	if err != nil || result.Type != state.Type {
		return nil, fmt.Errorf("stuntgres: invalid aggregate support signature")
	}
	provider := &exec.TransitionAggregate{Aggregate: d, Support: support, StateUsage: state, Result: resultUsage, Inputs: inputs, Formals: formals, Conversions: make([]sqltype.CoercionPlan, len(inputs))}
	if collatableInput && !inputInfo.CollationConflict {
		provider.InputCollation = inputInfo.Usage.Collation
	}
	var parameters []expr.Expr
	if support.SQL == nil {
		parameters = make([]expr.Expr, len(inputs))
	}
	for i, u := range inputs {
		provider.Conversions[i], err = catalog.AggregateConversion(d.Aggregate.BinaryRecipe, u, formals[i])
		if err != nil {
			return nil, err
		}
		if parameters != nil {
			parameterInfo := expr.Info{Usage: formals[i], Property: expr.Properties{ParameterDependent: true}}
			if formals[i].Collation.ID != 0 {
				parameterInfo = inputInfo
				parameterInfo.Usage = formals[i]
				parameterInfo.Property = expr.Properties{ParameterDependent: true}
			}
			parameters[i] = plan.ParameterExpr{Info: parameterInfo, Number: i + 1}
		}
	}
	if support.Strict && !d.Aggregate.Definition.HasInitial {
		provider.SeedConversion, err = catalog.AggregateConversion(d.Aggregate.BinaryRecipe, inputs[1], state)
		if err != nil {
			return nil, err
		}
	}
	if support.SQL == nil {
		provider.Call, err = b.BindSelectedOperatorRoutine(support, parameters, raw.Pos)
		if err != nil {
			return nil, err
		}
	}
	if provider.Call != nil && provider.Call.ResultUsage().Type != state.Type {
		return nil, fmt.Errorf("stuntgres: invalid aggregate support result")
	}
	if oid := d.Aggregate.Definition.Functions[1]; oid != 0 {
		descriptor, found := b.definitionDescriptor(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		final, valid := descriptor.(*catalog.Procedure)
		if !found || !valid {
			return nil, fmt.Errorf("stuntgres: missing aggregate final routine")
		}
		provider.Final = final
		finalRef := b.Host.CatalogOwner().Domain.Ref(b.BindingCatalog().Epoch(), final.Address())
		b.dependency(finalRef, final.Revision())
		provider.FinalInputs = inputs[:1]
		if d.Aggregate.Definition.FinalExtra {
			provider.FinalInputs = inputs
		}
		provider.FinalFormals, result, err = routine.AggregateSupportTypes(b.BindingCatalog(), b.DefinitionReader(), final, d.Aggregate, provider.FinalInputs)
		if err != nil || result.Type != resultUsage.Type {
			return nil, fmt.Errorf("stuntgres: invalid aggregate final signature")
		}
		provider.FinalConversions = make([]sqltype.CoercionPlan, len(provider.FinalInputs))
		var parameters []expr.Expr
		if final.SQL == nil {
			parameters = make([]expr.Expr, len(provider.FinalInputs))
		}
		for i, u := range provider.FinalInputs {
			provider.FinalConversions[i], err = catalog.AggregateConversion(d.Aggregate.BinaryRecipe, u, provider.FinalFormals[i])
			if err != nil {
				return nil, err
			}
			if parameters != nil {
				parameterInfo := expr.Info{Usage: provider.FinalFormals[i], Property: expr.Properties{ParameterDependent: true}}
				if parameterInfo.Usage.Collation.ID != 0 && collatableInput {
					parameterInfo = inputInfo
					parameterInfo.Usage = provider.FinalFormals[i]
					parameterInfo.Property = expr.Properties{ParameterDependent: true}
				}
				parameters[i] = plan.ParameterExpr{Info: parameterInfo, Number: i + 1}
			}
		}
		if final.SQL == nil {
			provider.FinalCall, err = b.BindSelectedOperatorRoutine(final, parameters, raw.Pos)
			if err != nil {
				return nil, err
			}
			if provider.FinalCall.ResultUsage().Type != resultUsage.Type {
				return nil, fmt.Errorf("stuntgres: invalid aggregate final result")
			}
		}
	}
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
