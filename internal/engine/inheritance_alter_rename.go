package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Metadata-only graph evolution shares the same one-stage atomic publication
// contract as shape ALTER, without running a physical row rewrite.
func (e *Engine) renameInheritanceColumnLocked(scope *StatementScope, op *Operation, auth AuthorizationView, root *catalog.Relation, column, destination string, only bool) (Result, error) {
	candidate, err := renameColumnCandidate(root, column, destination)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if only && inheritanceHasChildren(auth.Catalog, root.Address()) {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", fmt.Sprintf(`inherited column "%s" must be renamed in child tables too`, column))
	}
	targets, auth, err := scope.acquireInheritanceDefinitions(root, !only)
	if err != nil {
		return Result{}, err
	}
	if current := targets[0]; current != root {
		candidate, err = renameColumnCandidate(current, column, destination)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		root = current
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	selected := make(map[ids.ObjectAddress]bool, len(targets))
	for _, old := range targets {
		selected[old.Address()] = true
	}
	for _, old := range targets {
		next := candidate
		if old != root {
			slot, exists := old.Index[column]
			if !exists {
				return Result{}, ids.ErrCatalogReplaced
			}
			for _, origin := range old.Inheritance.Columns[old.Attributes[slot]].Origins {
				if !selected[origin.Parent] {
					return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited column "%s"`, column))
				}
			}
			next, err = renameColumnCandidate(old, column, destination)
			if err != nil {
				return Result{}, err
			}
		}
		if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.ObjectRevision = old.Revision() + 1
		if err := stage.Replace(next); err != nil {
			return Result{}, err
		}
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

func (e *Engine) renameInheritanceConstraintLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTableRenameConstraint, root *catalog.Relation, kind byte) (Result, error) {
	name := raw.Constraint.Text
	inherited := false
	noInherit := false
	if root.Inheritance != nil {
		if kind == 'c' {
			inherited = root.Inheritance.Checks[name].Count() != 0
		} else {
			for _, c := range root.Constraints.NotNull {
				if c.Name == name {
					inherited = root.Inheritance.NotNull[c.Attribute].Count() != 0
				}
			}
		}
	}
	if inherited {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
	}
	if kind == 'c' {
		for _, c := range root.Constraints.Check {
			if c.Name == name {
				noInherit = c.NoInherit
			}
		}
	} else {
		for _, c := range root.Constraints.NotNull {
			if c.Name == name {
				noInherit = c.NoInherit
			}
		}
	}
	if raw.Only && !noInherit && inheritanceHasChildren(auth.Catalog, root.Address()) {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", fmt.Sprintf(`inherited constraint "%s" must be renamed in child tables too`, name))
	}
	targets, auth, err := scope.acquireInheritanceDefinitions(root, !raw.Only && !noInherit)
	if err != nil {
		return Result{}, err
	}
	root = targets[0]
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	selected := make(map[ids.ObjectAddress]bool, len(targets))
	for _, old := range targets {
		selected[old.Address()] = true
	}
	for _, old := range targets {
		if old != root {
			provenance := old.Inheritance.Checks[name]
			if kind == 'n' {
				for _, c := range old.Constraints.NotNull {
					if c.Name == name {
						provenance = old.Inheritance.NotNull[c.Attribute]
					}
				}
			}
			if len(provenance.Retired) != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
			}
			for _, origin := range provenance.Origins {
				if !selected[origin.Parent] {
					return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
				}
			}
		}
		next, err := renameLocalConstraint(old, name, raw.NewName.Text, kind)
		if err != nil {
			return Result{}, err
		}
		if next.Inheritance != nil {
			next.Inheritance = cloneInheritance(next.Inheritance)
			if kind == 'c' {
				next.Inheritance.Checks[raw.NewName.Text] = next.Inheritance.Checks[name]
				delete(next.Inheritance.Checks, name)
			}
		}
		if err := stage.Replace(next); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

// The caller holds Engine.mu. Success retains it so the caller can create its
// sole stage from the returned authentic authorization/catalog without a gap;
// every error returns unlocked. AX waits always run outside Engine.mu.
func (scope *StatementScope) acquireInheritanceDefinitions(root *catalog.Relation, recurse bool) ([]*catalog.Relation, AuthorizationView, error) {
	e := scope.op.session.Engine()
	epoch := scope.lockedRef.Epoch
	captured := map[ids.ObjectAddress]*catalog.Relation{root.Address(): root}
	fail := func(err error) ([]*catalog.Relation, AuthorizationView, error) {
		e.mu.Unlock()
		return nil, AuthorizationView{}, err
	}
	for {
		if err := scope.op.Context().Err(); err != nil {
			return fail(err)
		}
		auth, err := e.authorizationLocked(scope.op)
		if err != nil {
			return fail(err)
		}
		view := auth.Catalog
		if view.Epoch() != epoch {
			return fail(ids.ErrCatalogReplaced)
		}
		for address, old := range captured {
			current, _ := view.Relation(address)
			if !inheritanceMarkerPublication(old, current) {
				return fail(ids.ErrCatalogReplaced)
			}
			if current != old {
				captured[address] = current
			}
		}
		root = captured[root.Address()]
		var targets []*catalog.Relation
		if recurse {
			targets = inheritanceAlterOrder(view, root)
		} else {
			targets = []*catalog.Relation{root}
		}
		var pending *catalog.Relation
		for _, old := range targets {
			ref := scope.lockedRef
			ref.Address = old.Address()
			if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
				return fail(err)
			}
			if captured[old.Address()] == nil && pending == nil {
				pending = old
			}
		}
		if pending == nil {
			return targets, auth, nil
		}
		captured[pending.Address()] = pending
		ref := scope.lockedRef
		ref.Address = pending.Address()
		e.mu.Unlock()
		if err := scope.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return nil, AuthorizationView{}, err
		}
		e.mu.Lock()
	}
}
