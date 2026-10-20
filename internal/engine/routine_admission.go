package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// Only an original binding lifetime can authorize a selected call. The opaque
// descriptor identifies the declaration; neither its ACL nor its address alone
// supplies authority. Admission borrows the immutable manifest without copying.
func admitRoutineDescriptor(auth AuthorizationView, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure, consumer *catalog.Relation) error {
	current, err := routine.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured)
	if err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if dependency.Ref.Address != captured.Address() {
			continue
		}
		if dependency.Revision != captured.Revision() {
			return fmt.Errorf("stuntgres: mismatched routine lifetime manifest")
		}
		ref := dependency.Ref
		if consumer != nil {
			if !storedReferenceEpochValid(auth.Catalog, consumer, ref) {
				return ids.ErrCatalogReplaced
			}
			ref.Epoch = auth.Catalog.Epoch()
		}
		if ref.Epoch != auth.Catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if _, err := auth.Object(ref); err != nil {
			return err
		}
		if current.Revision() != dependency.Revision {
			return catalog.Conflict{}
		}
		return auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege})
	}
	return fmt.Errorf("stuntgres: missing routine lifetime manifest")
}

// Called during serial planning admission, never by per-row Eval. Retset calls
// have their separate source/project demand point: a pruned FROM source must not
// acquire EXECUTE merely because its arguments were inspected during planning.
func admitRoutineCall(c *exec.Context, node expr.Expr, scalarPlanning, constantComputation bool) error {
	recipe, recipeErr := routine.ExpressionRecipeDescriptor(node)
	if recipeErr != nil {
		return recipeErr
	}
	if recipe != nil && (!c.RoutineGraft || c.RoutineConsumer != recipe) || recipe == nil && c.RoutineGraft {
		local := *c
		if err := selectExpressionRecipe(&local, recipe); err != nil {
			return err
		}
		c = &local
	}
	var pairs []plan.ComparisonExpr
	var ordering []sqltype.Comparison
	switch expression := node.(type) {
	case plan.RowComparisonExpr:
		pairs, ordering = expression.Comparisons, expression.Ordering
	case plan.SubqueryExpr:
		pairs, ordering = expression.Comparisons, expression.Ordering
	case plan.CaseExpr:
		if expression.Test != nil && !exec.RandomLiteralNull(expression.Test) {
			for _, arm := range expression.Arms {
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
	captured, err := routine.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		return err
	}
	if scalarPlanning && procedureReturnsSet(captured) {
		return nil
	}
	auth, dependencies, err := routineAdmissionAuthority(c)
	if err != nil || auth.engine == nil {
		return err
	}
	if strictScalarRoutine(captured) {
		null := false
		strictRoutineArguments(node, func(child expr.Expr) {
			if child != nil && exec.RandomLiteralNull(child) {
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

func admitCaseEquality(c *exec.Context, arm plan.CaseBranch) error {
	if arm.Routine == nil || arm.Routine.Strict && exec.RandomLiteralNull(arm.Condition) {
		return nil
	}
	auth, dependencies, err := routineAdmissionAuthority(c)
	if err != nil || auth.engine == nil {
		return err
	}
	return admitRecipeRoutineDescriptor(c, auth, dependencies, arm.Routine)
}

func admitRecipeRoutineDescriptor(c *exec.Context, auth AuthorizationView, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure) error {
	if err := checkRecipeRoutineDescriptor(c, auth, dependencies, captured); err != nil {
		return err
	}
	if program := routineProgramOf(c); program != nil {
		return program.record(c, dependencies, captured)
	}
	return nil
}

func checkRecipeRoutineDescriptor(c *exec.Context, auth AuthorizationView, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure) error {
	if c.RoutineChecks != nil {
		return admitRoutineDescriptor(auth, c.RoutineChecks, captured, c.RoutineConsumer)
	}
	if c.RoutineReferences != nil {
		if _, err := routine.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured); err != nil {
			return err
		}
		for _, ref := range c.RoutineReferences {
			if ref.Address != captured.Address() {
				continue
			}
			if !storedReferenceEpochValid(auth.Catalog, c.RoutineConsumer, ref) {
				return ids.ErrCatalogReplaced
			}
			// This exact retained recipe's established epoch bridge does not
			// change the object identity or create a reference from an address.
			ref.Epoch = auth.Catalog.Epoch()
			return auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege})
		}
		return fmt.Errorf("stuntgres: missing stored routine lifetime manifest")
	}
	if c.RoutineRecipe {
		return fmt.Errorf("stuntgres: empty owning routine lifetime manifest")
	}
	return admitRoutineDescriptor(auth, dependencies, captured, nil)
}

func routineAdmissionAuthority(c *exec.Context) (AuthorizationView, []catalog.ExpressionDependency, error) {
	var auth AuthorizationView
	var dependencies []catalog.ExpressionDependency
	if routineAuth := routineAuthorityView(c); routineAuth != nil {
		auth = *routineAuth
	} else if scope := scopeOf(c.LiveHost()); scope != nil {
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
	if c.RoutineRecipe {
		dependencies = c.RoutineChecks
	}
	return auth, dependencies, nil
}

// Aggregate/window carriers already contain the actual provenance-bearing ref.
// Their admission is once per executable graph occurrence, including empty
// inputs, and is independent of any comparator or aggregate support function.
func admitRoutineReference(c *exec.Context, ref ids.ObjectRef, noun string) error {
	auth, dependencies, err := routineAdmissionAuthority(c)
	if err != nil || auth.engine == nil {
		return err
	}
	if ref.Address.ClassID != ids.ProcedureClassID || ref.Address.ObjectID == 0 || ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid callable routine reference")
	}
	for _, dependency := range dependencies {
		if dependency.Ref != ref {
			continue
		}
		if c.RoutineConsumer != nil {
			if !storedReferenceEpochValid(auth.Catalog, c.RoutineConsumer, ref) {
				return ids.ErrCatalogReplaced
			}
			ref.Epoch = auth.Catalog.Epoch()
		}
		if ref.Epoch != auth.Catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		d, err := auth.Object(ref)
		if err != nil {
			return err
		}
		if d.Revision() != dependency.Revision {
			return catalog.Conflict{}
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
func evaluatePlanningExpression(c *exec.Context, node expr.Expr) (sqltype.Value, error) {
	var failure error
	plan.WalkExpression(node, func(call expr.Expr) {
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
func admitQueryGroupings(c *exec.Context, groups []plan.Grouping, keys []expr.Expr) error {
	if len(groups) != len(keys) {
		return fmt.Errorf("stuntgres: missing query grouping identity")
	}
	for i, group := range groups {
		private := privateTreeGrouping(keys[i].ResultUsage().Type)
		if private != group.PrivateTree {
			return fmt.Errorf("stuntgres: changed query grouping capability")
		}
		if private {
			if group.Routine.Address.ObjectID != 0 {
				return fmt.Errorf("stuntgres: native tree grouping has a callable identity")
			}
			continue
		}
		if err := admitRoutineReference(c, group.Routine, "function"); err != nil {
			return err
		}
	}
	return nil
}
