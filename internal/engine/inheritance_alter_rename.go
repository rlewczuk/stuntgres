package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Metadata-only graph evolution shares the same one-stage atomic publication
// contract as shape ALTER, without running a physical row rewrite.
func (e *Engine) renameInheritanceColumnLocked(scope *StatementScope, op *Operation, auth AuthorizationView, root *relationDescriptor, column, destination string, only bool) (Result, error) {
	candidate, err := renameColumnCandidate(root, column, destination)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if only && inheritanceHasChildren(auth.catalog, root.address) {
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
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	selected := make(map[ids.ObjectAddress]bool, len(targets))
	for _, old := range targets {
		selected[old.address] = true
	}
	for _, old := range targets {
		next := candidate
		if old != root {
			slot, exists := old.index[column]
			if !exists {
				return Result{}, ErrCatalogReplaced
			}
			for _, origin := range old.inheritance.columns[old.attributes[slot]].origins {
				if !selected[origin.parent] {
					return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited column "%s"`, column))
				}
			}
			next, err = renameColumnCandidate(old, column, destination)
			if err != nil {
				return Result{}, err
			}
		}
		if old.revision == ids.DefinitionRevision(^uint64(0)) {
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.revision = old.revision + 1
		if err := stage.Replace(next); err != nil {
			return Result{}, err
		}
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: auth.catalog.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

func (e *Engine) renameInheritanceConstraintLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTableRenameConstraint, root *relationDescriptor, kind byte) (Result, error) {
	name := raw.Constraint.Text
	inherited := false
	noInherit := false
	if root.inheritance != nil {
		if kind == 'c' {
			inherited = root.inheritance.checks[name].count() != 0
		} else {
			for _, c := range root.notNullConstraints {
				if c.Name == name {
					inherited = root.inheritance.notNull[c.Attribute].count() != 0
				}
			}
		}
	}
	if inherited {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
	}
	if kind == 'c' {
		for _, c := range root.checkConstraints {
			if c.Name == name {
				noInherit = c.NoInherit
			}
		}
	} else {
		for _, c := range root.notNullConstraints {
			if c.Name == name {
				noInherit = c.NoInherit
			}
		}
	}
	if raw.Only && !noInherit && inheritanceHasChildren(auth.catalog, root.address) {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", fmt.Sprintf(`inherited constraint "%s" must be renamed in child tables too`, name))
	}
	targets, auth, err := scope.acquireInheritanceDefinitions(root, !raw.Only && !noInherit)
	if err != nil {
		return Result{}, err
	}
	root = targets[0]
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	selected := make(map[ids.ObjectAddress]bool, len(targets))
	for _, old := range targets {
		selected[old.address] = true
	}
	for _, old := range targets {
		if old != root {
			provenance := old.inheritance.checks[name]
			if kind == 'n' {
				for _, c := range old.notNullConstraints {
					if c.Name == name {
						provenance = old.inheritance.notNull[c.Attribute]
					}
				}
			}
			if len(provenance.retired) != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
			}
			for _, origin := range provenance.origins {
				if !selected[origin.parent] {
					return Result{}, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
				}
			}
		}
		next, err := renameLocalConstraint(old, name, raw.NewName.Text, kind)
		if err != nil {
			return Result{}, err
		}
		if next.inheritance != nil {
			next.inheritance = cloneInheritance(next.inheritance)
			if kind == 'c' {
				next.inheritance.checks[raw.NewName.Text] = next.inheritance.checks[name]
				delete(next.inheritance.checks, name)
			}
		}
		if err := stage.Replace(next); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: auth.catalog.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

// The caller holds Engine.mu. Success retains it so the caller can create its
// sole stage from the returned authentic authorization/catalog without a gap;
// every error returns unlocked. AX waits always run outside Engine.mu.
func (scope *StatementScope) acquireInheritanceDefinitions(root *relationDescriptor, recurse bool) ([]*relationDescriptor, AuthorizationView, error) {
	e := scope.op.session.Engine()
	epoch := scope.lockedRef.Epoch
	captured := map[ids.ObjectAddress]*relationDescriptor{root.address: root}
	fail := func(err error) ([]*relationDescriptor, AuthorizationView, error) {
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
		catalog := auth.catalog
		if catalog.Epoch() != epoch {
			return fail(ErrCatalogReplaced)
		}
		for address, old := range captured {
			current, _ := catalog.Relation(address)
			if !inheritanceMarkerPublication(old, current) {
				return fail(ErrCatalogReplaced)
			}
			if current != old {
				captured[address] = current
			}
		}
		root = captured[root.address]
		var targets []*relationDescriptor
		if recurse {
			targets = inheritanceAlterOrder(catalog, root)
		} else {
			targets = []*relationDescriptor{root}
		}
		var pending *relationDescriptor
		for _, old := range targets {
			ref := scope.lockedRef
			ref.Address = old.address
			if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
				return fail(err)
			}
			if captured[old.address] == nil && pending == nil {
				pending = old
			}
		}
		if pending == nil {
			return targets, auth, nil
		}
		captured[pending.address] = pending
		ref := scope.lockedRef
		ref.Address = pending.address
		e.mu.Unlock()
		if err := scope.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return nil, AuthorizationView{}, err
		}
		e.mu.Lock()
	}
}
