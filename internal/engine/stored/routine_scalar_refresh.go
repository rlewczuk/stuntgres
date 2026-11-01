package stored

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// RefreshStoredScalarRoutine is a publication operation, not historical remapping.
// The DDL owner supplies its immutable pre-mutation read and checked private
// successor. Returned descriptors must join that same stage or all be discarded.
// No executable value, row, physical index entry or retained source is changed.
func RefreshStoredScalarRoutine(source, target catalog.Read, before, after *catalog.Procedure, revision func() (ids.DefinitionRevision, error)) (*ScalarRoutinePublication, error) {
	if source.State() == nil || target.State() == nil || source.Owner() == nil || source.Owner() != target.Owner() ||
		source.Owner().Domain == nil || source.Owner().Principals == nil || source.Epoch() != target.Epoch() ||
		before == nil || after == nil || before == after || revision == nil {
		return nil, ids.ErrCatalogReplaced
	}
	if source.Get(before.Address()) != before || target.Get(after.Address()) != after ||
		before.Address() != after.Address() || before.Namespace() != after.Namespace() || before.Name() != after.Name() ||
		before.Signature != after.Signature || before.RoutineKind != catalog.FunctionRoutine || after.RoutineKind != before.RoutineKind ||
		before.Result != after.Result || before.Owner != after.Owner || before.Extension != after.Extension || !authorization.EqualACL(before.ACL, after.ACL) ||
		before.RoutineDefinition() == nil || after.RoutineDefinition() == nil || after.Revision() <= before.Revision() {
		return nil, fmt.Errorf("stuntgres: invalid routine replacement publication")
	}
	if err := (ProcedureProvider{}).Validate(source, before); err != nil {
		return nil, err
	}
	if err := (ProcedureProvider{}).Validate(target, after); err != nil {
		return nil, err
	}
	op := scalarRoutineRefresh{source: source, target: target, before: before, after: after}
	var changed []catalog.Descriptor
	for _, descriptor := range source.State().Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		next, err := op.relation(relation)
		if err != nil {
			return nil, err
		}
		if next == relation {
			continue
		}
		if target.Get(relation.Address()) != relation {
			return nil, ids.ErrCatalogReplaced
		}
		next.ObjectRevision, err = revision()
		if err != nil {
			return nil, err
		}
		changed = append(changed, next)
	}
	// Validate the complete private result before returning any installable set.
	// The source and caller's target remain untouched on every failure.
	completed := target
	if len(changed) != 0 {
		state := catalog.CloneState(target.State())
		for _, descriptor := range changed {
			state.SetDescriptor(descriptor.Address(), descriptor)
		}
		view := target.WithState(state)
		completed = view
		for _, descriptor := range changed {
			provider := view.Owner().Provider(descriptor.Address().ClassID)
			if provider == nil {
				return nil, ids.ErrCatalogReplaced
			}
			if err := provider.Validate(view, descriptor); err != nil {
				return nil, err
			}
		}
	}
	publication := &ScalarRoutinePublication{source: source, completed: completed, descriptors: changed, before: before, after: after}
	for _, descriptor := range changed {
		relation := descriptor.(*catalog.Relation)
		relation.ScalarPublication = publication
	}
	return publication, nil
}

type scalarRoutineRefresh struct {
	source, target catalog.Read
	before, after  *catalog.Procedure
}

func (r *scalarRoutineRefresh) expression(node expr.Expr, refs []ids.ObjectRef, dependencies []catalog.ExpressionDependency) (expr.Expr, bool, error) {
	needed := false
	var invalid error
	plan.WalkExpression(node, func(node expr.Expr) {
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok || call.Ref.Address != r.before.Address() {
			return
		}
		needed = true
		if err := call.ValidateStoredProvider(); err != nil {
			invalid = err
			return
		}
		if _, err := currentRoutineCall(r.source, call); err != nil {
			invalid = err
			return
		}
		if !exec.SameRoutineDeclaration(r.before, call.Procedure) {
			invalid = ids.ErrCatalogReplaced
			return
		}
		found := false
		for _, ref := range refs {
			found = found || ref == call.Ref
		}
		for _, dep := range dependencies {
			if dep.Ref == call.Ref {
				if dep.Revision != call.Procedure.Revision() || dep.Purpose != catalog.DefinitionDependency {
					invalid = fmt.Errorf("stuntgres: mismatched routine replacement dependency")
				}
				found = true
			}
		}
		if !found {
			invalid = fmt.Errorf("stuntgres: missing routine replacement dependency")
		}
	})
	if invalid != nil {
		return nil, false, invalid
	}
	if !needed {
		return node, false, nil
	}
	identity := func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }
	original := func(p *catalog.Procedure) (*catalog.Procedure, error) {
		if _, err := exec.ValidateStoredRoutineDescriptor(r.source, p); err != nil {
			return nil, err
		}
		if _, err := exec.ValidateStoredRoutineDescriptor(r.target, p); err != nil {
			return nil, err
		}
		return p, nil
	}
	var mapNode func(expr.Expr) (expr.Expr, bool, error)
	leaf := func(node expr.Expr) (expr.Expr, bool, error) {
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok || call.Ref.Address != r.before.Address() {
			return node, false, nil
		}
		if err := call.ValidateStoredProvider(); err != nil {
			return nil, false, err
		}
		if _, err := currentRoutineCall(r.source, call); err != nil {
			return nil, false, err
		}
		if !exec.SameRoutineDeclaration(r.before, call.Procedure) || !call.Signature.ValidFor(r.after.RoutineDefinition()) {
			return nil, false, ids.ErrCatalogReplaced
		}
		var args []expr.Expr
		for i, arg := range call.Args {
			next, changed, err := mapNode(arg)
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
		if args == nil {
			args = call.Args
		}
		call.Procedure, call.RoutineReference = r.after, r.after
		call.Property = call.GroupProperties(args)
		next, err := call.Rebuild(call.Info, args)
		if err != nil {
			return nil, false, err
		}
		call = next.(plan.SQLRoutineExpr)
		if err := call.ValidateStoredProvider(); err != nil {
			return nil, false, err
		}
		if _, err := currentRoutineCall(r.target, call); err != nil {
			return nil, false, err
		}
		return call, true, nil
	}
	mapNode = func(node expr.Expr) (expr.Expr, bool, error) {
		return mapExpressionWithProcedure(r.target, nil, node, identity, nil, nil, false, leaf, original, nil)
	}
	return mapNode(node)
}

func (r *scalarRoutineRefresh) relation(old *catalog.Relation) (*catalog.Relation, error) {
	var next *catalog.Relation
	copyRelation := func() error {
		if next != nil {
			return nil
		}
		provider := r.source.Owner().Provider(old.Address().ClassID)
		if provider == nil {
			return ids.ErrCatalogReplaced
		}
		if err := provider.Validate(r.source, old); err != nil {
			return err
		}
		copy := *old
		next = &copy
		return nil
	}
	defaultsCopied := false
	for attribute, definition := range old.ColumnMeta.Defaults {
		node, changed, err := r.expression(definition.Expression, definition.Dependencies, nil)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if err := copyRelation(); err != nil {
			return nil, err
		}
		if !defaultsCopied {
			defaults := make(map[int32]catalog.ColumnDefault, len(old.ColumnMeta.Defaults))
			for key, value := range next.ColumnMeta.Defaults {
				defaults[key] = value
			}
			next.ColumnMeta.Defaults = defaults
			defaultsCopied = true
		}
		definition.Expression = node
		next.ColumnMeta.Defaults[attribute] = definition
	}
	checksCopied := false
	for i, check := range old.Constraints.Check {
		node, changed, err := r.expression(check.Expression, nil, check.Dependencies)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if err := copyRelation(); err != nil {
			return nil, err
		}
		if !checksCopied {
			next.Constraints.Check = slices.Clone(old.Constraints.Check)
			checksCopied = true
		}
		check.Expression = node
		check.Dependencies = slices.Clone(check.Dependencies)
		for j := range check.Dependencies {
			if check.Dependencies[j].Ref.Address == r.before.Address() {
				check.Dependencies[j].Revision = r.after.Revision()
			}
		}
		next.Constraints.Check[i] = check
	}
	if old.SQLIndex != nil {
		var index *catalog.IndexDefinition
		copyIndex := func() error {
			if index != nil {
				return nil
			}
			if err := copyRelation(); err != nil {
				return err
			}
			copy := *old.SQLIndex
			index = &copy
			next.SQLIndex = index
			return nil
		}
		keysCopied := false
		for i, key := range old.SQLIndex.Keys {
			if key.Derived == nil {
				continue
			}
			definition, a, err := r.expression(key.Derived.Definition, key.Derived.Dependencies, nil)
			if err != nil {
				return nil, err
			}
			execution, b, err := r.expression(key.Derived.Execution, key.Derived.Dependencies, nil)
			if err != nil {
				return nil, err
			}
			if !a && !b {
				continue
			}
			if err := copyIndex(); err != nil {
				return nil, err
			}
			if !keysCopied {
				index.Keys = slices.Clone(old.SQLIndex.Keys)
				keysCopied = true
			}
			derived := *key.Derived
			derived.Definition, derived.Execution = definition, execution
			index.Keys[i].Derived = &derived
		}
		if predicate := old.SQLIndex.Predicate; predicate != nil {
			definition, a, err := r.expression(predicate.Definition, predicate.Dependencies, nil)
			if err != nil {
				return nil, err
			}
			execution, b, err := r.expression(predicate.Execution, predicate.Dependencies, nil)
			if err != nil {
				return nil, err
			}
			if a || b {
				if err := copyIndex(); err != nil {
					return nil, err
				}
				copy := *predicate
				copy.Definition, copy.Execution = definition, execution
				index.Predicate = &copy
			}
		}
	}
	if next == nil {
		return old, nil
	}
	return next, nil
}
