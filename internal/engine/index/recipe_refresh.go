package index

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// refreshIndexRecipe binds executable calls to current declarations through the
// exact owning index recipe's identity manifest. A replaced function body is
// not an index-definition replacement. This does not alter the stored tree,
// resolve SQL names again, or relax ordinary retained-statement carrier checks.
func refreshIndexRecipe(evaluation *exec.Context, parent *catalog.Relation, dependencies []ids.ObjectRef, expression expr.Expr) (expr.Expr, error) {
	view := evaluation.Catalog
	// Body-free recipes keep their original cooked executor and provider seam.
	// Only a manifest containing SQL callables can need declaration refresh.
	hasSQLRoutine := false
	for _, dependency := range dependencies {
		if dependency.Address.ClassID != ids.ProcedureClassID {
			continue
		}
		if view.State() == nil {
			// Internal recipe callers can carry only a live statement host.
			// Resolve its canonical admission catalog once, using the same
			// authority for refresh, coercion preflight and executable admission.
			auth, _, err := exec.RoutineAdmissionAuthority(evaluation)
			if err != nil {
				return nil, err
			}
			if !auth.Live() || auth.Catalog.State() == nil || auth.Catalog.Owner() == nil {
				return nil, fmt.Errorf("stuntgres: index recipe lacks catalog admission authority")
			}
			view = auth.Catalog
			evaluation.Catalog = view
		}
		if routine, ok := view.Procedure(dependency.Address); ok && routine.SQL != nil {
			hasSQLRoutine = true
			break
		}
	}
	if !hasSQLRoutine {
		return expression, nil
	}
	var refresh func(expr.Expr) (expr.Expr, bool, error)
	refresh = func(node expr.Expr) (expr.Expr, bool, error) {
		return stored.MapExpression(view, node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, func(node expr.Expr) (expr.Expr, bool, error) {
			call, ok := node.(plan.SQLRoutineExpr)
			if !ok {
				return node, false, nil
			}
			if err := call.ValidateStoredProvider(); err != nil {
				return nil, false, err
			}
			if !slices.Contains(dependencies, call.Ref) || !stored.ReferenceEpochValid(view, parent, call.Ref) || call.Procedure.Owner.ID() == 0 || call.Procedure.Owner != view.Owner().Principals.Principal(call.Procedure.Owner.ID()) {
				return nil, false, ids.ErrCatalogReplaced
			}
			current, ok := view.Procedure(call.Ref.Address)
			if !ok || call.Procedure.SQL == nil || current.SQL == nil || current.RoutineDefinition() == nil || current.Signature != call.Procedure.Signature || current.RoutineKind != call.Procedure.RoutineKind || !call.Signature.ValidFor(current.RoutineDefinition()) {
				return nil, false, fmt.Errorf("stuntgres: incompatible current index routine declaration")
			}
			var args []expr.Expr
			for i, arg := range call.Args {
				next, changed, err := refresh(arg)
				if err != nil {
					return nil, false, err
				}
				if changed {
					if args == nil {
						args = slices.Clone(call.Args)
					}
					args[i] = next
				}
			}
			if args == nil && exec.SameRoutineDeclaration(current, call.Procedure) {
				return node, false, nil
			}
			if args == nil {
				args = call.Args
			}
			call.Procedure = current
			call.Ref.Epoch = view.Epoch()
			call.Info.RoutineReference = current
			call.Info.Property = call.GroupProperties(args)
			rebuilt, err := call.Rebuild(call.Info, args)
			if err != nil {
				return nil, false, err
			}
			if err := rebuilt.(plan.SQLRoutineExpr).ValidateStoredProvider(); err != nil {
				return nil, false, err
			}
			return rebuilt, true, nil
		})
	}
	next, _, err := refresh(expression)
	return next, err
}
