package exec

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Only an original binding lifetime can authorize a selected call. The opaque
// descriptor identifies the declaration; neither its ACL nor its address alone
// supplies authority. Admission borrows the immutable manifest without copying.
func AdmitRoutineDescriptor(auth RoutineAccess, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure, consumer *catalog.Relation) error {
	return admitRoutineDescriptor(auth, dependencies, captured, consumer, false, catalog.DefinitionDependency)
}

// A hard stored recipe proves its original manifest and immutable declaration.
// Mutable name/schema/owner revisions do not reinterpret that captured call.
// Original-source binding keeps its existing current-revision fence.
func admitRoutineDescriptor(auth RoutineAccess, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure, consumer *catalog.Relation, followIdentity bool, purpose catalog.DependencyPurpose) error {
	ref, err := routineDescriptorLifetime(auth, dependencies, captured, consumer, followIdentity, purpose)
	if err != nil {
		return err
	}
	return auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege})
}

// Lifetime validation is independent of invocation demand. A strict NULL call
// can avoid EXECUTE, but cannot rehabilitate a stale direct-analysis graph.
func routineDescriptorLifetime(auth RoutineAccess, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure, consumer *catalog.Relation, followIdentity bool, purpose catalog.DependencyPurpose) (ids.ObjectRef, error) {
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	for _, dependency := range dependencies {
		if dependency.Ref.Address != captured.Address() || dependency.Revision != captured.Revision() || dependency.Purpose != purpose {
			continue
		}
		ref := dependency.Ref
		if consumer != nil {
			if !preparation.StoredReferenceValid(auth.Catalog, consumer, ref) {
				return ids.ObjectRef{}, ids.ErrCatalogReplaced
			}
			ref.Epoch = auth.Catalog.Epoch()
		}
		if ref.Epoch != auth.Catalog.Epoch() {
			return ids.ObjectRef{}, ids.ErrCatalogReplaced
		}
		if _, err := auth.Object(ref); err != nil {
			return ids.ObjectRef{}, err
		}
		if !followIdentity && current.Revision() != dependency.Revision {
			return ids.ObjectRef{}, catalog.Conflict{}
		}
		return ref, nil
	}
	return ids.ObjectRef{}, fmt.Errorf("stuntgres: missing routine lifetime manifest")
}

// Called during serial planning admission, never by per-row Eval. Retset calls
// have their separate source/project demand point: a pruned FROM source must not
// acquire EXECUTE merely because its arguments were inspected during planning.
func AdmitRoutineCall(c *Context, node expr.Expr, scalarPlanning, constantComputation bool) error {
	recipe, recipeErr := ExpressionRecipeDescriptor(node)
	if recipeErr != nil {
		return recipeErr
	}
	if recipe != nil && (!c.RoutineGraft || c.RoutineGraftSource != recipe) || recipe == nil && c.RoutineGraft {
		local := *c
		if err := SelectExpressionRecipe(&local, recipe); err != nil {
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
		if expression.Test != nil {
			for _, arm := range expression.Arms {
				// A nonstrict selected test conversion can turn a saved NULL
				// into a non-NULL equality operand. Only a retained fixed
				// recipe preserves the old literal-NULL admission shortcut.
				if _, fixed := plan.FixedCaseTest(arm.Test); fixed && RandomLiteralNull(expression.Test) {
					continue
				}
				if err := AdmitCaseEquality(c, arm); err != nil {
					return err
				}
			}
		}
	}
	// Multi-field row ordering invokes BTORDER support, not the scalar
	// oprcode. One-field rows and row equality retain ordinary checked calls.
	if len(ordering) == 0 || len(pairs) == 1 {
		for _, pair := range pairs {
			if err := AdmitRoutineCall(c, pair, scalarPlanning, constantComputation); err != nil {
				return err
			}
		}
	}
	captured, err := ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		return err
	}
	if scalarPlanning && preparation.ReturnsSet(captured) {
		return nil
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil || !auth.Live() {
		return err
	}
	// Slot STRICT semantics belong to invocation, after routine admission.
	// In particular, a NULL drop parameter must not erase its EXECUTE check.
	_, slotCall := node.(functions.ReplicationSlot)
	if StrictScalarRoutine(captured) && !slotCall {
		null := false
		StrictRoutineArguments(node, func(child expr.Expr) {
			if child != nil && RandomLiteralNull(child) && (!c.PreserveParameterDemand || !child.Properties().ParameterDependent) {
				null = true
			}
		})
		if null {
			if c.RoutineChecks == nil && c.RoutineReferences == nil && !c.RoutineRecipe {
				_, err := routineDescriptorLifetime(auth, dependencies, captured, nil, false, catalog.DefinitionDependency)
				return err
			}
			return nil
		}
	}
	if err := AdmitRecipeRoutineDescriptor(c, auth, dependencies, captured); err != nil {
		return err
	}
	if inquiryRoutine(captured) {
		return validateInquiryBodyLifetime(c, node, captured)
	}
	return admitRoutineBody(c, node, captured, constantComputation)
}

func AdmitCaseEquality(c *Context, arm plan.CaseBranch) error {
	if arm.Routine == nil || arm.Routine.Strict && RandomLiteralNull(arm.Condition) {
		return nil
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil || !auth.Live() {
		return err
	}
	return AdmitRecipeRoutineDescriptor(c, auth, dependencies, arm.Routine)
}

func AdmitRecipeRoutineDescriptor(c *Context, auth RoutineAccess, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure) error {
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, captured); err != nil {
		return err
	}
	if program := c.RoutineProgram; program != nil {
		return program.Record(c, dependencies, captured)
	}
	return nil
}

func CheckRecipeRoutineDescriptor(c *Context, auth RoutineAccess, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure) error {
	return checkRecipeRoutineDescriptor(c, auth, dependencies, captured, catalog.DefinitionDependency)
}

func checkRecipeRoutineDescriptor(c *Context, auth RoutineAccess, dependencies []catalog.ExpressionDependency, captured *catalog.Procedure, purpose catalog.DependencyPurpose) error {
	if c.RoutineChecks != nil {
		return admitRoutineDescriptor(auth, c.RoutineChecks, captured, c.RoutineConsumer, true, purpose)
	}
	if c.RoutineReferences != nil {
		if _, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured); err != nil {
			return err
		}
		for _, ref := range c.RoutineReferences {
			if ref.Address != captured.Address() {
				continue
			}
			if !preparation.StoredReferenceValid(auth.Catalog, c.RoutineConsumer, ref) {
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
	return admitRoutineDescriptor(auth, dependencies, captured, nil, false, purpose)
}

func RoutineAdmissionAuthority(c *Context) (RoutineAccess, []catalog.ExpressionDependency, error) {
	var auth RoutineAccess
	var dependencies []catalog.ExpressionDependency
	if c.RoutineAuthority != nil {
		auth = c.RoutineAuthority.AdmissionAccess()
	} else if host := c.LiveHost(); host != nil {
		var err error
		if auth, dependencies, err = host.RoutineAdmission(); err != nil {
			return auth, nil, err
		}
	} else {
		// Standalone private provider fixtures have no SQL admission owner.
		return auth, nil, nil
	}
	if !auth.Live() {
		return auth, nil, fmt.Errorf("stuntgres: missing live routine admission authority")
	}
	if c.RoutineRecipe {
		dependencies = c.RoutineChecks
	}
	return auth, dependencies, nil
}

// Aggregate/window carriers contain their original ref. Where they also retain
// the original descriptor, hard recipes can follow a checked immutable
// declaration across mutable revisions. Ref-only and source lanes stay strict.
func AdmitRoutineReference(c *Context, ref ids.ObjectRef, captured *catalog.Procedure, noun string) error {
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil || !auth.Live() {
		return err
	}
	if ref.Address.ClassID != ids.ProcedureClassID || ref.Address.ObjectID == 0 || ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid callable routine reference")
	}
	for _, dependency := range dependencies {
		if dependency.Ref != ref || dependency.Purpose != catalog.DefinitionDependency || captured != nil && captured.Revision() != dependency.Revision {
			continue
		}
		if c.RoutineConsumer != nil {
			if !preparation.StoredReferenceValid(auth.Catalog, c.RoutineConsumer, ref) {
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
		if captured != nil {
			if captured.Address() != ref.Address || captured.Revision() != dependency.Revision {
				return fmt.Errorf("stuntgres: mismatched callable lifetime manifest")
			}
			if _, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured); err != nil {
				return err
			}
		}
		if d.Revision() != dependency.Revision && (captured == nil || c.RoutineChecks == nil && c.RoutineReferences == nil) {
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

// A preflight computation can consume this execution's imported parameters.
// At an opted-in PL diagnostic boundary, record the actual failing Eval,
// including constants, so an outer computation cannot accidentally inherit
// an inner callee's parameter-execution origin.
type planningEvaluationError struct {
	error
	parameters bool
}

func (e *planningEvaluationError) Unwrap() error                      { return e.error }
func (e *planningEvaluationError) PreflightParameterEvaluation() bool { return e.parameters }

func admitPlanningExpression(c *Context, node expr.Expr) error {
	var failure error
	plan.WalkExpression(node, func(call expr.Expr) {
		if failure == nil {
			failure = AdmitRoutineCall(c, call, true, true)
		}
	})
	return failure
}

func evaluateAdmittedPlanningExpression(c *Context, node expr.Expr) (sqltype.Value, error) {
	previous := c.ConstantPlanning
	c.ConstantPlanning = true
	defer func() { c.ConstantPlanning = previous }()
	value, err := node.Eval(c)
	if err != nil && c != nil && c.PLPreflightOrigin {
		err = &planningEvaluationError{error: err, parameters: node.Properties().ParameterDependent}
	}
	return value, err
}

// Only evaluator-local stack copies select graft ancestry. A user replacement
// cannot inherit the enclosing stored node's manifest or relation owner.
func SelectExpressionRecipe(c *Context, reference expr.RoutineRecipeReference) error {
	c.RoutineGraft = reference != nil
	c.RoutineGraftSource = reference
	c.RoutineRecipe = reference != nil
	c.RoutineReferences = nil
	c.RoutineChecks = nil
	c.RoutineConsumer = nil
	if reference == nil {
		return nil
	}
	owner, presentation, err := routineRecipeParts(reference)
	if err != nil {
		return err
	}
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	if _, rule := reference.(*catalog.RuleRoutineRecipe); rule && !auth.Live() {
		return ids.ErrCatalogReplaced
	}
	if auth.Live() {
		current, ok := auth.Catalog.Relation(owner.Address())
		if !ok || owner.Owner.ID() == 0 || auth.Roles.ValidatePrincipal(owner.Owner) != nil {
			return ids.ErrCatalogReplaced
		}
		switch recipe := reference.(type) {
		case *catalog.Relation:
			if current.View != presentation {
				return ids.ErrCatalogReplaced
			}
		case *catalog.RuleRoutineRecipe:
			if current.Rules == nil || current.Rules.RoutineRecipe(recipe.Rule, recipe.Action) != presentation {
				return ids.ErrCatalogReplaced
			}
			if consumer, temporary := presentation.TemporaryConsumer(); consumer != (ids.ObjectAddress{}) || temporary != (ids.SessionToken{}) {
				namespace, ok := auth.Catalog.Namespace(current.Namespace())
				if !ok || consumer != current.Address() || temporary == (ids.SessionToken{}) || temporary != namespace.Temporary || temporary.Domain() != auth.Owner.Domain {
					return ids.ErrCatalogReplaced
				}
			}
		}
	}
	c.RoutineConsumer = owner
	c.RoutineChecks = presentation.Dependencies()
	return nil
}

// PostgreSQL leaves RECORD calls unreduced so their concrete tuple description
// survives, even for a strict known-NULL input (evaluate_function, REL_18_0).
// Scalar calls may disappear in either target or FROM position; SRFs may not.
func StrictScalarRoutine(routine *catalog.Procedure) bool {
	return routine != nil && routine.Strict && routine.RoutineKind == catalog.FunctionRoutine &&
		routine.Result != 2249 && !preparation.ReturnsSet(routine)
}

// Expanded variadic elements form one non-NULL array argument. A NULL
// element is not a NULL formal; explicit VARIADIC retains the array child.
func StrictRoutineArguments(node expr.Expr, visit func(expr.Expr)) {
	if expression, ok := node.(plan.SQLRoutineExpr); ok {
		for child, slot := range expression.ChildSlots {
			if !expression.InputMap[slot].Expanded {
				visit(expression.Args[child])
			}
		}
		return
	}
	if expression, ok := node.(functions.JSONExtract); ok && !expression.Variadic {
		visit(expression.Args[0])
		return
	}
	if expression, ok := node.(plan.RangeConstructorExpr); ok && !expression.Variadic &&
		expression.Procedure != nil && expression.Procedure.RangeConstructor != nil &&
		expression.Procedure.RangeConstructor.Form == catalog.MultirangeConstructorMany {
		// This declaration has only its variadic array formal. Expanded
		// range members, including NULLs, are not nullable call formals.
		return
	}
	plan.ExpressionChildren(node, visit)
}
