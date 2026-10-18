package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Only an original binding lifetime can authorize a selected call. The opaque
// descriptor identifies the declaration; neither its ACL nor its address alone
// supplies authority. Admission borrows the immutable manifest without copying.
func admitRoutineDescriptor(auth AuthorizationView, dependencies []expressionDependency, captured *procedureDescriptor, consumer *relationDescriptor) error {
	current, err := validateRoutineDescriptor(auth.catalog, auth.roles, captured)
	if err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if dependency.ref.Address != captured.address {
			continue
		}
		if dependency.revision != captured.revision {
			return fmt.Errorf("stuntgres: mismatched routine lifetime manifest")
		}
		ref := dependency.ref
		if consumer != nil {
			if !storedReferenceEpochValid(auth.catalog, consumer, ref) {
				return ids.ErrCatalogReplaced
			}
			ref.Epoch = auth.catalog.Epoch()
		}
		if ref.Epoch != auth.catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if _, err := auth.object(ref); err != nil {
			return err
		}
		if current.revision != dependency.revision {
			return CatalogConflict{}
		}
		return auth.Check(AccessRequirement{Object: ref, Privileges: authorization.ExecutePrivilege})
	}
	return fmt.Errorf("stuntgres: missing routine lifetime manifest")
}

// Called during serial planning admission, never by per-row Eval. Retset calls
// have their separate source/project demand point: a pruned FROM source must not
// acquire EXECUTE merely because its arguments were inspected during planning.
func admitRoutineCall(c *EvalContext, node expr.Expr, scalarPlanning, constantComputation bool) error {
	recipe, recipeErr := expressionRecipeDescriptor(node)
	if recipeErr != nil {
		return recipeErr
	}
	if recipe != nil && (!c.routineGraft || c.routineConsumer != recipe) || recipe == nil && c.routineGraft {
		local := *c
		if err := local.selectExpressionRecipe(recipe); err != nil {
			return err
		}
		c = &local
	}
	var pairs []comparisonExpression
	var ordering []sqltype.Comparison
	switch expression := node.(type) {
	case rowComparisonExpression:
		pairs, ordering = expression.comparisons, expression.ordering
	case subqueryExpression:
		pairs, ordering = expression.comparisons, expression.ordering
	case caseExpression:
		if expression.test != nil && !randomLiteralNull(expression.test) {
			for _, arm := range expression.arms {
				if err := admitCaseEquality(c, arm); err != nil {
					return err
				}
			}
		}
	}
	// Multi-field row ordering invokes BTORDER support, not the scalar
	// oprcode. One-field rows and row equality retain ordinary checked calls.
	if len(ordering) == 0 || len(pairs) == 1 {
		for _, pair := range pairs {
			if err := admitRoutineCall(c, pair, scalarPlanning, constantComputation); err != nil {
				return err
			}
		}
	}
	captured, err := expressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		return err
	}
	if scalarPlanning && captured.returnsSet() {
		return nil
	}
	auth, dependencies, err := c.routineAdmissionAuthority()
	if err != nil || auth.engine == nil {
		return err
	}
	if strictScalarRoutine(captured) {
		null := false
		strictRoutineArguments(node, func(child expr.Expr) {
			if child != nil && randomLiteralNull(child) {
				null = true
			}
		})
		if null {
			return nil
		}
	}
	if err := admitRecipeRoutineDescriptor(c, auth, dependencies, captured); err != nil {
		return err
	}
	if inquiryRoutine(captured) {
		return validateInquiryBodyLifetime(c, node, captured)
	}
	return admitRoutineBody(c, node, captured, constantComputation)
}

func admitCaseEquality(c *EvalContext, arm caseBranch) error {
	if arm.routine == nil || arm.routine.strict && randomLiteralNull(arm.condition) {
		return nil
	}
	auth, dependencies, err := c.routineAdmissionAuthority()
	if err != nil || auth.engine == nil {
		return err
	}
	return admitRecipeRoutineDescriptor(c, auth, dependencies, arm.routine)
}

func admitRecipeRoutineDescriptor(c *EvalContext, auth AuthorizationView, dependencies []expressionDependency, captured *procedureDescriptor) error {
	if err := checkRecipeRoutineDescriptor(c, auth, dependencies, captured); err != nil {
		return err
	}
	if c.routineProgram != nil {
		return c.routineProgram.record(c, dependencies, captured)
	}
	return nil
}

func checkRecipeRoutineDescriptor(c *EvalContext, auth AuthorizationView, dependencies []expressionDependency, captured *procedureDescriptor) error {
	if c.routineChecks != nil {
		return admitRoutineDescriptor(auth, c.routineChecks, captured, c.routineConsumer)
	}
	if c.routineReferences != nil {
		if _, err := validateRoutineDescriptor(auth.catalog, auth.roles, captured); err != nil {
			return err
		}
		for _, ref := range c.routineReferences {
			if ref.Address != captured.address {
				continue
			}
			if !storedReferenceEpochValid(auth.catalog, c.routineConsumer, ref) {
				return ids.ErrCatalogReplaced
			}
			// This exact retained recipe's established epoch bridge does not
			// change the object identity or create a reference from an address.
			ref.Epoch = auth.catalog.Epoch()
			return auth.Check(AccessRequirement{Object: ref, Privileges: authorization.ExecutePrivilege})
		}
		return fmt.Errorf("stuntgres: missing stored routine lifetime manifest")
	}
	if c.routineRecipe {
		return fmt.Errorf("stuntgres: empty owning routine lifetime manifest")
	}
	return admitRoutineDescriptor(auth, dependencies, captured, nil)
}

func (c *EvalContext) routineAdmissionAuthority() (AuthorizationView, []expressionDependency, error) {
	var auth AuthorizationView
	var dependencies []expressionDependency
	if c.routineAuthority != nil {
		auth = *c.routineAuthority
	} else if scope := c.liveScope(); scope != nil {
		if scope.finished {
			return auth, nil, fmt.Errorf("stuntgres: ended routine admission authority")
		}
		auth, dependencies = scope.routineAuth, scope.routineDependencies
	} else {
		// Standalone private provider fixtures have no SQL admission owner.
		return auth, nil, nil
	}
	if auth.engine == nil {
		return auth, nil, fmt.Errorf("stuntgres: missing live routine admission authority")
	}
	if c.routineRecipe {
		dependencies = c.routineChecks
	}
	return auth, dependencies, nil
}

// Aggregate/window carriers already contain the actual provenance-bearing ref.
// Their admission is once per executable graph occurrence, including empty
// inputs, and is independent of any comparator or aggregate support function.
func admitRoutineReference(c *EvalContext, ref ids.ObjectRef, noun string) error {
	auth, dependencies, err := c.routineAdmissionAuthority()
	if err != nil || auth.engine == nil {
		return err
	}
	if ref.Address.ClassID != ids.ProcedureClassID || ref.Address.ObjectID == 0 || ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid callable routine reference")
	}
	for _, dependency := range dependencies {
		if dependency.ref != ref {
			continue
		}
		if c.routineConsumer != nil {
			if !storedReferenceEpochValid(auth.catalog, c.routineConsumer, ref) {
				return ids.ErrCatalogReplaced
			}
			ref.Epoch = auth.catalog.Epoch()
		}
		if ref.Epoch != auth.catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		d, err := auth.object(ref)
		if err != nil {
			return err
		}
		if d.Revision() != dependency.revision {
			return CatalogConflict{}
		}
		rights, err := auth.Effective(ref, false)
		if err != nil {
			return err
		}
		if rights&authorization.ExecutePrivilege == 0 {
			return sqlError("42501", "permission denied for "+noun+" "+d.Name())
		}
		return nil
	}
	return fmt.Errorf("stuntgres: missing callable routine lifetime manifest")
}

// A planning-only computation boundary, not a runtime expression wrapper.
// Child folds have already discarded non-invocations and cleared their carrier;
// still-live callable children must be admitted before an enclosing Eval can
// invoke them. No permission lookup is added to row evaluation.
func evaluatePlanningExpression(c *EvalContext, node expr.Expr) (sqltype.Value, error) {
	var failure error
	walkBoundExpression(node, func(call expr.Expr) {
		if failure == nil {
			failure = admitRoutineCall(c, call, true, true)
		}
	})
	if failure != nil {
		return sqltype.Value{}, failure
	}
	return node.Eval(c)
}

// Query equality is caller-checked; index/hash support and native semantic-tree
// grouping are deliberately not callable PostgreSQL routines.
func admitQueryGroupings(c *EvalContext, groups []queryGrouping, keys []expr.Expr) error {
	if len(groups) != len(keys) {
		return fmt.Errorf("stuntgres: missing query grouping identity")
	}
	for i, group := range groups {
		private := privateTreeGrouping(keys[i].ResultUsage().Type)
		if private != group.privateTree {
			return fmt.Errorf("stuntgres: changed query grouping capability")
		}
		if private {
			if group.routine.Address.ObjectID != 0 {
				return fmt.Errorf("stuntgres: native tree grouping has a callable identity")
			}
			continue
		}
		if err := admitRoutineReference(c, group.routine, "function"); err != nil {
			return err
		}
	}
	return nil
}
