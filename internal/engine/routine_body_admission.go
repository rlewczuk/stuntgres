package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type routineBodyContext struct {
	outer     *catalog.Procedure
	statement bool
}

func (context routineBodyContext) annotate(err error) error {
	if err == nil || context.outer == nil {
		return err
	}
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
		copy := *diagnostic
		phase := "during inlining"
		if context.statement {
			phase = "statement 1"
		}
		copy.Context = fmt.Sprintf(`SQL function "%s" %s`, context.outer.Name(), phase)
		return &copy
	}
	return err
}
func bodyErrorContext(outer *catalog.Procedure, constant bool) routineBodyContext {
	if !constant || outer.Address().ObjectID == 3935 || outer.Address().ObjectID == 3936 {
		return routineBodyContext{}
	}
	return routineBodyContext{outer: outer, statement: outer.Volatility == expr.Immutable}
}
func inquiryRoutine(outer *catalog.Procedure) bool {
	switch outer.Address().ObjectID {
	case 1215, 1216, 1348, 1993:
		return true
	}
	return false
}

// The declaration edge chooses a candidate, not its lifetime. A current owned
// descriptor can be selected only after matching the exact original manifest.
func bodyRoutineDescriptor(c *EvalContext, auth AuthorizationView, dependencies []catalog.ExpressionDependency, edge catalog.RoutineBodyEdge) (*catalog.Procedure, error) {
	var original ids.ObjectRef
	var revision ids.DefinitionRevision
	if c.routineReferences != nil {
		for _, ref := range c.routineReferences {
			if ref.Address == edge.Routine.Address() {
				original = ref
				break
			}
		}
	} else {
		if c.routineChecks != nil {
			dependencies = c.routineChecks
		}
		for _, dependency := range dependencies {
			if dependency.Ref.Address == edge.Routine.Address() {
				original, revision = dependency.Ref, dependency.Revision
				break
			}
		}
	}
	if original.Address != edge.Routine.Address() {
		return nil, fmt.Errorf("stuntgres: missing original SQL body lifetime manifest")
	}
	ref := original
	if c.routineConsumer != nil {
		if !storedReferenceEpochValid(auth.Catalog, c.routineConsumer, ref) {
			return nil, ids.ErrCatalogReplaced
		}
		ref.Epoch = auth.Catalog.Epoch()
	}
	if ref.Epoch != auth.Catalog.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	descriptor, err := auth.Object(ref)
	if err != nil {
		return nil, err
	}
	current, ok := descriptor.(*catalog.Procedure)
	if !ok || !sameRoutineDeclaration(current, edge.Routine) {
		return nil, fmt.Errorf("stuntgres: stale SQL body declaration")
	}
	if revision != 0 && current.Revision() != revision {
		return nil, catalog.Conflict{}
	}
	return current, nil
}

func validateInquiryBodyLifetime(c *EvalContext, node expr.Expr, outer *catalog.Procedure) error {
	auth, dependencies, err := c.routineAdmissionAuthority()
	if err != nil || auth.engine == nil {
		return err
	}
	return walkRoutineBody(outer, node, func(body *catalog.Procedure) error {
		_, err := bodyRoutineDescriptor(c, auth, dependencies, catalog.RoutineBodyEdge{Routine: body})
		return err
	})
}

func bodyEdgeDemanded(node expr.Expr, outer *catalog.Procedure, edge catalog.RoutineBodyEdge) bool {
	switch edge.Demand {
	case catalog.BodyBooleanText, catalog.BodyNetworkText:
		argument := polymorphicBodyArgument(node, outer.Address().ObjectID)
		return bodyCastMatches(edge, argument) && !randomLiteralNull(argument)
	case catalog.BodyLeftDuration, catalog.BodyRightDuration:
		overlap, ok := node.(functions.DatetimeOverlaps)
		if !ok {
			return false
		}
		at := 0
		if edge.Demand == catalog.BodyRightDuration {
			at = 2
		}
		return !randomLiteralNull(overlap.Args[at]) && !randomLiteralNull(overlap.Args[at+1])
	}
	return true
}

func admitRoutineBody(c *EvalContext, node expr.Expr, outer *catalog.Procedure, constant bool) error {
	if len(outer.Body) == 0 || inquiryRoutine(outer) {
		return nil
	}
	auth, dependencies, err := c.routineAdmissionAuthority()
	if err != nil || auth.engine == nil {
		return err
	}
	if !constant && node != nil {
		// Imported immutable scalar parameters are execution-frame constants.
		// Classify their SQL-body diagnostics without evaluating arguments again.
		p := node.Properties()
		constant = p.ParameterDependent && p.Volatility == expr.Immutable &&
			!p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion
	}
	local := *c
	local.routineBodyContext = bodyErrorContext(outer, constant)
	for _, edge := range outer.Body {
		if !bodyEdgeDemanded(node, outer, edge) {
			continue
		}
		body, err := bodyRoutineDescriptor(&local, auth, dependencies, edge)
		if err == nil {
			err = admitRecipeRoutineDescriptor(&local, auth, dependencies, body)
		}
		if err != nil {
			return local.routineBodyContext.annotate(err)
		}
		if err = admitRoutineBody(&local, nil, body, constant); err != nil {
			return err
		}
	}
	return nil
}

func admitOverlapsPair(c *EvalContext, e functions.DatetimeOverlaps, pair int) error {
	outer, err := expressionRoutineDescriptor(e)
	if err != nil || outer == nil {
		return err
	}
	auth, dependencies, err := c.routineAdmissionAuthority()
	if err != nil || auth.engine == nil {
		return err
	}
	if err = admitRecipeRoutineDescriptor(c, auth, dependencies, outer); err != nil {
		return err
	}
	local := *c
	local.routineBodyContext = routineBodyContext{outer: outer}
	demand := catalog.BodyLeftDuration
	if pair == 1 {
		demand = catalog.BodyRightDuration
	}
	for _, edge := range outer.Body {
		if edge.Demand != demand || !bodyEdgeDemanded(e, outer, edge) {
			continue
		}
		body, err := bodyRoutineDescriptor(&local, auth, dependencies, edge)
		if err == nil {
			err = admitRecipeRoutineDescriptor(&local, auth, dependencies, body)
		}
		return local.routineBodyContext.annotate(err)
	}
	return nil
}
