package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type routineBodyContext struct {
	outer     *procedureDescriptor
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
		copy.Context = fmt.Sprintf(`SQL function "%s" %s`, context.outer.name, phase)
		return &copy
	}
	return err
}
func bodyErrorContext(outer *procedureDescriptor, constant bool) routineBodyContext {
	if !constant || outer.address.ObjectID == 3935 || outer.address.ObjectID == 3936 {
		return routineBodyContext{}
	}
	return routineBodyContext{outer: outer, statement: outer.volatility == Immutable}
}
func inquiryRoutine(outer *procedureDescriptor) bool {
	switch outer.address.ObjectID {
	case 1215, 1216, 1348, 1993:
		return true
	}
	return false
}

// The declaration edge chooses a candidate, not its lifetime. A current owned
// descriptor can be selected only after matching the exact original manifest.
func bodyRoutineDescriptor(c *EvalContext, auth AuthorizationView, dependencies []expressionDependency, edge routineBodyEdge) (*procedureDescriptor, error) {
	var original ids.ObjectRef
	var revision ids.DefinitionRevision
	if c.routineReferences != nil {
		for _, ref := range c.routineReferences {
			if ref.Address == edge.routine.address {
				original = ref
				break
			}
		}
	} else {
		if c.routineChecks != nil {
			dependencies = c.routineChecks
		}
		for _, dependency := range dependencies {
			if dependency.ref.Address == edge.routine.address {
				original, revision = dependency.ref, dependency.revision
				break
			}
		}
	}
	if original.Address != edge.routine.address {
		return nil, fmt.Errorf("stuntgres: missing original SQL body lifetime manifest")
	}
	ref := original
	if c.routineConsumer != nil {
		if !storedReferenceEpochValid(auth.catalog, c.routineConsumer, ref) {
			return nil, ErrCatalogReplaced
		}
		ref.Epoch = auth.catalog.Epoch()
	}
	if ref.Epoch != auth.catalog.Epoch() {
		return nil, ErrCatalogReplaced
	}
	descriptor, err := auth.object(ref)
	if err != nil {
		return nil, err
	}
	current, ok := descriptor.(*procedureDescriptor)
	if !ok || !sameRoutineDeclaration(current, edge.routine) {
		return nil, fmt.Errorf("stuntgres: stale SQL body declaration")
	}
	if revision != 0 && current.revision != revision {
		return nil, CatalogConflict{}
	}
	return current, nil
}

func validateInquiryBodyLifetime(c *EvalContext, node BoundExpr, outer *procedureDescriptor) error {
	auth, dependencies, err := c.routineAdmissionAuthority()
	if err != nil || auth.engine == nil {
		return err
	}
	return walkRoutineBody(outer, node, func(body *procedureDescriptor) error {
		_, err := bodyRoutineDescriptor(c, auth, dependencies, routineBodyEdge{routine: body})
		return err
	})
}

func bodyEdgeDemanded(node BoundExpr, outer *procedureDescriptor, edge routineBodyEdge) bool {
	switch edge.demand {
	case bodyBooleanText, bodyNetworkText:
		argument := polymorphicBodyArgument(node, outer.address.ObjectID)
		return bodyCastMatches(edge, argument) && !randomLiteralNull(argument)
	case bodyLeftDuration, bodyRightDuration:
		overlap, ok := node.(functions.DatetimeOverlaps[BoundExpr, ExprContext, ExprProperties])
		if !ok {
			return false
		}
		at := 0
		if edge.demand == bodyRightDuration {
			at = 2
		}
		return !randomLiteralNull(overlap.Args[at]) && !randomLiteralNull(overlap.Args[at+1])
	}
	return true
}

func admitRoutineBody(c *EvalContext, node BoundExpr, outer *procedureDescriptor, constant bool) error {
	if len(outer.body) == 0 || inquiryRoutine(outer) {
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
		constant = p.ParameterDependent && p.Volatility == Immutable &&
			!p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion
	}
	local := *c
	local.routineBodyContext = bodyErrorContext(outer, constant)
	for _, edge := range outer.body {
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

func admitOverlapsPair(c *EvalContext, e functions.DatetimeOverlaps[BoundExpr, ExprContext, ExprProperties], pair int) error {
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
	demand := bodyLeftDuration
	if pair == 1 {
		demand = bodyRightDuration
	}
	for _, edge := range outer.body {
		if edge.demand != demand || !bodyEdgeDemanded(e, outer, edge) {
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
