package exec

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// nativeAggregateSource exists only on TransitionAggregate.invoke's local
// evaluator until its selected kernel enters. A value copy cannot adopt self.
// It is neither an ordinary callsite receipt nor a change of SQL actor.
type nativeAggregateSource struct {
	self       *Context
	owner      RoutineInitialization
	read       catalog.Read
	aggregate  *catalog.Procedure
	support    *catalog.Procedure
	reference  expr.RoutineReference
	recipe     expr.RoutineRecipeReference
	occurrence uint32
	source     expr.FoldSourceOccurrence
	purpose    catalog.DependencyPurpose
}

func (c *Context) consumeNativeAggregateSource(reference expr.RoutineReference, recipe expr.RoutineRecipeReference, inputs *expr.RoutineBodyInputs) (nativeAggregateSource, error) {
	source := c.nativeAggregate
	// Consume before validation too: a failed entry cannot leave reusable state.
	c.nativeAggregate = nativeAggregateSource{}
	read := c.Catalog
	declaration, valid := reference.(*catalog.Procedure)
	if source.self != c || source.owner != c.RoutineInitialization ||
		source.read.Owner() != read.Owner() || source.read.Domain() != read.Domain() || source.read.Epoch() != read.Epoch() || source.read.State() != read.State() ||
		source.aggregate == nil || source.support == nil || !valid || declaration == nil ||
		declaration != source.support && !SameRoutineDeclaration(declaration, source.support) ||
		source.reference != reference || source.recipe != recipe ||
		source.occurrence != inputs.Occurrence || source.source != inputs.Source || source.purpose != catalog.AggregateSupportDependency ||
		inputs.AggregateOwner != nil && inputs.AggregateOwner != source.aggregate {
		return nativeAggregateSource{}, fmt.Errorf("stuntgres: mismatched immediate native aggregate source")
	}
	active, failure := routineSimulationActive(c)
	if failure != nil {
		return nativeAggregateSource{}, failure
	}
	if active {
		if !source.owner.Active() {
			return nativeAggregateSource{}, fmt.Errorf("stuntgres: mismatched immediate native aggregate source")
		}
		if c.ClosedEvaluation != nil {
			if err := c.ClosedEvaluation.Routines.checkSource(source.owner, read); err != nil {
				return nativeAggregateSource{}, err
			}
		} else {
			host := c.LiveHost()
			if host == nil {
				return nativeAggregateSource{}, fmt.Errorf("stuntgres: missing native aggregate invocation owner")
			}
			if err := host.ValidateRoutineInitializationOwner(source.owner, read); err != nil {
				return nativeAggregateSource{}, err
			}
		}
	}
	inputs.AggregateOwner = source.aggregate
	c.nativeAggregateEntered = true
	source.self = nil // The authenticated local continuation cannot adopt an evaluator.
	return source, nil
}

// FunctionEnterRoutine consumes the original selected declaration and recipe at
// the owning kernel's postargument body boundary. It never resolves names,
// evaluates children, or retains borrowed argument storage.
func (c *Context) FunctionEnterRoutine(reference expr.RoutineReference, recipe expr.RoutineRecipeReference, inputs expr.RoutineBodyInputs) (result sqltype.RoutineBodyToken, err error) {
	var parent nativeAggregateSource
	var aggregateParent *nativeAggregateSource
	if c != nil && c.nativeAggregate.self != nil {
		parent, err = c.consumeNativeAggregateSource(reference, recipe, &inputs)
		if err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
		aggregateParent = &parent
	}
	if reference == nil {
		return sqltype.RoutineBodyToken{}, nil
	}
	declaration, ok := reference.(*catalog.Procedure)
	if !ok || declaration == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: invalid native routine reference")
	}
	if c == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing native routine invocation authority")
	}
	if c.ControlLiteralAuthority.present() {
		return c.enterControlLiteral(declaration, recipe)
	}
	owner := c.Catalog.Owner()
	active, failure := routineSimulationActive(c)
	if failure != nil {
		return sqltype.RoutineBodyToken{}, failure
	}
	if !active && (aggregateParent == nil && declaration.DeclarationBody == nil || declaration.Strict && inputs.HasNull) {
		return sqltype.RoutineBodyToken{}, nil
	}
	selected := c
	var local Context
	if recipe != nil {
		local = *c
		selected = &local
		if err = SelectExpressionRecipe(selected, recipe); err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
	}
	if !active && aggregateParent == nil {
		if err = initializeNativeBody(selected, declaration, inputs, nil); err != nil {
			return sqltype.RoutineBodyToken{}, bodyInitializationError(selected, declaration, err)
		}
		return sqltype.RoutineBodyToken{}, nil
	}
	var aggregate *catalog.Procedure
	var auth RoutineAccess
	var dependencies []catalog.ExpressionDependency
	if inputs.AggregateOwner == nil {
		info := expr.Info{Occurrence: inputs.Occurrence, Source: inputs.Source}
		if err = validateRoutineExpressionInitialization(selected, declaration, info); err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
	} else {
		aggregate, ok = inputs.AggregateOwner.(*catalog.Procedure)
		if !ok || aggregate == nil {
			return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: invalid aggregate support owner reference")
		}
		auth, dependencies, err = RoutineAdmissionAuthority(selected)
		if err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
		if !auth.Live() {
			return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing aggregate support source authority")
		}
		if _, _, err = ValidateAggregateSupportMembership(auth.Catalog, auth.Roles, aggregate, declaration); err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
		if _, _, err = recipeRoutineLifetime(selected, auth, dependencies, declaration, catalog.AggregateSupportDependency); err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
	}
	// Execute initialization and original lifetime were proved above, even for
	// original fmgrtab fast paths. STRICT suppresses only the actual body.
	if declaration.Strict && inputs.HasNull {
		return sqltype.RoutineBodyToken{}, nil
	}
	if !active {
		if err = initializeNativeBody(selected, declaration, inputs, aggregateParent); err != nil {
			return sqltype.RoutineBodyToken{}, bodyInitializationError(selected, declaration, err)
		}
		return sqltype.RoutineBodyToken{}, nil
	}
	if routine.FmgrBuiltin(declaration) {
		return sqltype.RoutineBodyToken{}, nil
	}
	if c.ClosedEvaluation != nil {
		if owner.Sepgsql.AnyTrustedTransitions() || len(declaration.Body) != 0 {
			return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: native body requires serial invocation authority")
		}
		return sqltype.RoutineBodyToken{}, nil
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing native body invocation authority")
	}
	auth, dependencies, err = RoutineInvocationAuthority(selected)
	if err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if observer := auth.Sepgsql.Observer; observer != nil {
		mark := observer.MarkSepgsqlObservations()
		defer func() {
			if err != nil {
				observer.RestoreSepgsqlObservations(mark)
			}
		}()
	}
	var token sqltype.RoutineBodyToken
	if aggregate != nil {
		token, err = host.EnterNativeAggregateSupport(aggregate, declaration)
	} else {
		token, err = host.EnterNativeRoutine(declaration)
	}
	if err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if !inquiryRoutine(declaration) {
		if err = initializeNativeBody(selected, declaration, inputs, aggregateParent); err != nil {
			token.Leave()
			return sqltype.RoutineBodyToken{}, bodyInitializationError(selected, declaration, err)
		}
	}
	return token, nil
}

// Diagnostics follow the authentic current declaration on the error path only.
// Original source descriptors and receipt keys remain unchanged.
func bodyInitializationError(c *Context, outer *catalog.Procedure, failure error) error {
	if auth, _, err := RoutineInvocationAuthority(c); err == nil && auth.Live() {
		if current, err := ValidateStoredRoutineDescriptor(auth.Catalog, outer); err == nil {
			outer = current
		}
	}
	if outer.DeclarationBody != nil {
		return SQLRoutineStatementContext(failure, outer.Name(), 1)
	}
	return bodyErrorContext(outer, c.ConstantPlanning).Annotate(failure)
}

// Body is the immutable released expression-initialization occurrence recipe.
// Its original INTERNAL children do not transition subjects. Initialize under
// the already-entered wrapper before its fused native kernels/conversions, not
// under the argument caller. This models the retained body, not PG custom-plan
// fold counts or universal custom/generic-plan diagnostic ordering.
func initializeNativeBody(c *Context, outer *catalog.Procedure, inputs expr.RoutineBodyInputs, aggregateParent *nativeAggregateSource) error {
	if len(outer.Body) == 0 && outer.DeclarationBody == nil {
		return nil
	}
	auth, dependencies, err := RoutineInvocationAuthority(c)
	if err != nil {
		return err
	}
	if !auth.Live() {
		return nil
	} // Standalone pure inactive kernels have no SQL principal.
	body, err := beginNativeBodyInitialization(c, outer, expr.Info{Occurrence: inputs.Occurrence, Source: inputs.Source}, NativeBodyInitialization, aggregateParent)
	if err != nil {
		return err
	}
	if len(outer.Body) == 0 {
		return initializeNativeDeclarationBody(&body, outer, expr.Info{Occurrence: inputs.Occurrence, Source: inputs.Source}, c.declarationDemand)
	}
	for ordinal, edge := range outer.Body {
		if edge.Demand == catalog.BodyLeftDuration && inputs.LiteralNullDurationPairs&1 != 0 ||
			edge.Demand == catalog.BodyRightDuration && inputs.LiteralNullDurationPairs&2 != 0 {
			continue
		}
		if edge.Demand == catalog.BodyBooleanText || edge.Demand == catalog.BodyNetworkText {
			input := inputs.First
			if outer.Address().ObjectID == 2003 {
				input = inputs.Second
			}
			if input == nil {
				return fmt.Errorf("stuntgres: missing SQL body polymorphic input")
			}
			id := input.BaseType().ID()
			if edge.Demand == catalog.BodyBooleanText && id != sqltype.Bool || edge.Demand == catalog.BodyNetworkText && id != sqltype.Inet && id != sqltype.Cidr {
				continue
			}
		}
		inner, err := BodyRoutineDescriptor(&body, auth, dependencies, edge)
		if err != nil {
			return err
		}
		if err = AdmitInitializedBodyRoutine(&body, auth, dependencies, inner, uint32(ordinal+1)); err != nil {
			return err
		}
	}
	return nil
}
