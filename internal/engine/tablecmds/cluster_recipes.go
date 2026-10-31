package tablecmds

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// A maintenance rebuild follows an existing stored call's identity, not its
// historical SQL body. Only this parent's private index recipes are refreshed;
// replacement/remapping and ordinary captured-program fences remain unchanged.
type clusterRecipeRefresh struct {
	evaluation *exec.Context
	parent     *catalog.Relation
	stage      *core.CatalogStage
	procedures map[ids.ObjectAddress]*catalog.Procedure
}

func (r *clusterRecipeRefresh) current(call plan.SQLRoutineExpr, dependencies []ids.ObjectRef) (*catalog.Procedure, error) {
	if err := call.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	view := r.evaluation.Catalog
	if !slices.Contains(dependencies, call.Ref) || !stored.ReferenceEpochValid(view, r.parent, call.Ref) || call.Procedure.Owner.ID() == 0 || call.Procedure.Owner != view.Owner().Principals.Principal(call.Procedure.Owner.ID()) {
		return nil, ids.ErrCatalogReplaced
	}
	current, cached := r.procedures[call.Ref.Address]
	if !cached {
		var exists bool
		current, exists = view.Procedure(call.Ref.Address)
		if !exists {
			return nil, catalog.Conflict{}
		}
	}
	if current.Signature != call.Procedure.Signature || current.RoutineKind != call.Procedure.RoutineKind || current.Result != call.Procedure.Result || current.RoutineDefinition() == nil || !call.Signature.ValidFor(current.RoutineDefinition()) {
		return nil, catalog.Conflict{}
	}
	if !cached {
		if _, err := exec.ValidateStoredRoutineDescriptor(view, current); err != nil {
			return nil, err
		}
		if r.stage.Base.Get(current.Address()) != current {
			return nil, catalog.Conflict{}
		}
		if r.procedures == nil {
			r.procedures = make(map[ids.ObjectAddress]*catalog.Procedure)
		}
		r.procedures[current.Address()] = current
		r.stage.DomainBases = append(r.stage.DomainBases, current)
	}
	return current, nil
}

func (r *clusterRecipeRefresh) definition(node expr.Expr, dependencies []ids.ObjectRef) (expr.Expr, bool, error) {
	changed := false
	var failure error
	var inspect func(expr.Expr)
	inspect = func(node expr.Expr) {
		if node == nil || failure != nil {
			return
		}
		if call, ok := node.(plan.SQLRoutineExpr); ok {
			current, err := r.current(call, dependencies)
			if err != nil {
				failure = err
				return
			}
			changed = changed || !exec.SameRoutineDeclaration(current, call.Procedure)
		}
		plan.ExpressionChildren(node, inspect)
	}
	inspect(node)
	if failure != nil || !changed {
		return node, false, failure
	}
	var refresh func(expr.Expr) (expr.Expr, bool, error)
	refresh = func(node expr.Expr) (expr.Expr, bool, error) {
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok {
			return node, false, nil
		}
		current, err := r.current(call, dependencies)
		if err != nil {
			return nil, false, err
		}
		args := call.Args
		copied := false
		for i, arg := range call.Args {
			next, changed, err := stored.MapExpression(r.evaluation.Catalog, arg, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, refresh)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if !copied {
					args = slices.Clone(args)
					copied = true
				}
				args[i] = next
			}
		}
		if !copied && exec.SameRoutineDeclaration(current, call.Procedure) {
			return node, false, nil
		}
		call.Procedure, call.Info.RoutineReference = current, current
		// This replaces the declaration itself, not just its children. Rebuild
		// exact call properties so lower current volatility does not retain
		// historical analyzed demand from the previous routine declaration.
		call.Info.Property = call.GroupProperties(args)
		rebuilt, err := call.Rebuild(call.Info, args)
		if err != nil {
			return nil, false, err
		}
		if err := rebuilt.(plan.SQLRoutineExpr).ValidateStoredProvider(); err != nil {
			return nil, false, err
		}
		return rebuilt, true, nil
	}
	return stored.MapExpression(r.evaluation.Catalog, node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, refresh)
}

func (r *clusterRecipeRefresh) expression(node expr.Expr, dependencies []ids.ObjectRef) (expr.Expr, expr.Expr, bool, error) {
	definition, changed, err := r.definition(node, dependencies)
	if err != nil || !changed {
		return node, nil, false, err
	}
	local := index.RecipeEvaluation(r.evaluation, dependencies, r.parent)
	var admission exec.RoutineProgram
	local.RoutineProgram = &admission
	execution, _, err := exec.PreflightCoercionsWithPolicy(&local, definition, true)
	if err == nil {
		execution, _, err = exec.SimplifyConditional(&local, execution, true)
	}
	r.evaluation.PreparedDomainChecks = local.PreparedDomainChecks
	if err != nil {
		return nil, nil, false, err
	}
	return definition, execution, true, nil
}

func refreshClusterIndexRecipes(e *core.Engine, evaluation *exec.Context, parent *catalog.Relation, stage *core.CatalogStage) error {
	refresh := clusterRecipeRefresh{evaluation: evaluation, parent: parent, stage: stage}
	for address, relation := range stage.StageRead().Relations() {
		if relation.SQLIndex == nil || relation.SQLIndex.Parent != parent.Address() {
			continue
		}
		definition := *relation.SQLIndex
		changed, keysCopied := false, false
		for i, key := range definition.Keys {
			if key.Derived == nil {
				continue
			}
			source, execution, replaced, err := refresh.expression(key.Derived.Definition, key.Derived.Dependencies)
			if err != nil {
				return err
			}
			if !replaced {
				continue
			}
			if !keysCopied {
				definition.Keys = slices.Clone(definition.Keys)
				keysCopied = true
			}
			derived := *key.Derived
			derived.Definition, derived.Execution = source, execution
			definition.Keys[i].Derived = &derived
			changed = true
		}
		if predicate := definition.Predicate; predicate != nil {
			source, execution, replaced, err := refresh.expression(predicate.Definition, predicate.Dependencies)
			if err != nil {
				return err
			}
			if replaced {
				copy := *predicate
				copy.Definition, copy.Execution = source, execution
				definition.Predicate, changed = &copy, true
			}
		}
		if !changed {
			continue
		}
		next := *relation
		next.SQLIndex = &definition
		base, ok := stage.Base.Relation(address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if relation.Revision() == base.Revision() {
			if err := table.BumpAlterRevision(relation, &next); err != nil {
				return err
			}
		}
		e.Mu.Lock()
		err := stage.Replace(&next)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
	}
	return evaluation.Context.Err()
}
