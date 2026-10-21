package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func bodyErrorContext(outer *catalog.Procedure, constant bool) exec.RoutineBodyContext {
	if !constant || outer.Address().ObjectID == 3935 || outer.Address().ObjectID == 3936 {
		return exec.RoutineBodyContext{}
	}
	return exec.RoutineBodyContext{Outer: outer, Statement: outer.Volatility == expr.Immutable}
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
func bodyRoutineDescriptor(c *exec.Context, auth AuthorizationView, dependencies []catalog.ExpressionDependency, edge catalog.RoutineBodyEdge) (*catalog.Procedure, error) {
	var original ids.ObjectRef
	var revision ids.DefinitionRevision
	if c.RoutineReferences != nil {
		for _, ref := range c.RoutineReferences {
			if ref.Address == edge.Routine.Address() {
				original = ref
				break
			}
		}
	} else {
		if c.RoutineChecks != nil {
			dependencies = c.RoutineChecks
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
	if c.RoutineConsumer != nil {
		if !storedReferenceEpochValid(auth.Catalog, c.RoutineConsumer, ref) {
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
	if !ok || !exec.SameRoutineDeclaration(current, edge.Routine) {
		return nil, fmt.Errorf("stuntgres: stale SQL body declaration")
	}
	if revision != 0 && current.Revision() != revision {
		return nil, catalog.Conflict{}
	}
	return current, nil
}

func validateInquiryBodyLifetime(c *exec.Context, node expr.Expr, outer *catalog.Procedure) error {
	auth, dependencies, err := routineAdmissionAuthority(c)
	if err != nil || auth.engine == nil {
		return err
	}
	return exec.WalkRoutineBody(outer, node, func(body *catalog.Procedure) error {
		_, err := bodyRoutineDescriptor(c, auth, dependencies, catalog.RoutineBodyEdge{Routine: body})
		return err
	})
}

func bodyEdgeDemanded(node expr.Expr, outer *catalog.Procedure, edge catalog.RoutineBodyEdge) bool {
	switch edge.Demand {
	case catalog.BodyBooleanText, catalog.BodyNetworkText:
		argument := exec.PolymorphicBodyArgument(node, outer.Address().ObjectID)
		return exec.BodyCastMatches(edge, argument) && !exec.RandomLiteralNull(argument)
	case catalog.BodyLeftDuration, catalog.BodyRightDuration:
		overlap, ok := node.(functions.DatetimeOverlaps)
		if !ok {
			return false
		}
		at := 0
		if edge.Demand == catalog.BodyRightDuration {
			at = 2
		}
		return !exec.RandomLiteralNull(overlap.Args[at]) && !exec.RandomLiteralNull(overlap.Args[at+1])
	}
	return true
}

func admitRoutineBody(c *exec.Context, node expr.Expr, outer *catalog.Procedure, constant bool) error {
	if len(outer.Body) == 0 || inquiryRoutine(outer) {
		return nil
	}
	auth, dependencies, err := routineAdmissionAuthority(c)
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
	local.RoutineBodyContext = bodyErrorContext(outer, constant)
	for _, edge := range outer.Body {
		if !bodyEdgeDemanded(node, outer, edge) {
			continue
		}
		body, err := bodyRoutineDescriptor(&local, auth, dependencies, edge)
		if err == nil {
			err = admitRecipeRoutineDescriptor(&local, auth, dependencies, body)
		}
		if err != nil {
			return local.RoutineBodyContext.Annotate(err)
		}
		if err = admitRoutineBody(&local, nil, body, constant); err != nil {
			return err
		}
	}
	return nil
}

func admitOverlapsPair(c *exec.Context, e functions.DatetimeOverlaps, pair int) error {
	outer, err := exec.ExpressionRoutineDescriptor(e)
	if err != nil || outer == nil {
		return err
	}
	auth, dependencies, err := routineAdmissionAuthority(c)
	if err != nil || auth.engine == nil {
		return err
	}
	if err = admitRecipeRoutineDescriptor(c, auth, dependencies, outer); err != nil {
		return err
	}
	local := *c
	local.RoutineBodyContext = exec.RoutineBodyContext{Outer: outer}
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
		return local.RoutineBodyContext.Annotate(err)
	}
	return nil
}
