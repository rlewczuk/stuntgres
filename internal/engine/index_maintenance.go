package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeIndexMaintenance(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if scope.txRef.epoch != e.state.epoch {
		return fail(ErrCatalogReplaced)
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return fail(err)
	}
	g := scope.indexMaintenance
	if g == nil {
		return fail(fmt.Errorf("stuntgres: index utility has no relation admission"))
	}
	if err := g.check(auth, stmt, true); err != nil {
		return fail(err)
	}
	if err := scope.captureIdentityLocked(auth); err != nil {
		return fail(err)
	}
	_, reindex := stmt.(*parser.Reindex)
	if len(g.targets) == 0 || reindex && len(g.indexes) == 0 {
		scope.op.session.mu.Lock()
		err := publicationOperationLocked(e, scope.op)
		scope.op.session.mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.completed = true
		if reindex {
			scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`table "%s" has no indexes to reindex`, g.descriptors[g.targets[0].Address].name)})
		}
		for _, notice := range g.notices {
			scope.op.emitNotice(levelNotice, notice)
		}
		return Result{}, nil
	}
	if s, ok := stmt.(*parser.AlterIndex); ok {
		return e.renameRelationLocked(scope, scope.op, auth, s.Name, "", s.Target.Text, s.IfExists, false, count)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = scope.op.session, auth.revision, auth.roles.state.revision
	stage.indexOutput = scope.output
	stage.indexParents = make(map[ObjectAddress]*relationDescriptor, len(g.parents))
	for _, ref := range g.parents {
		stage.indexParents[ref.Address] = g.descriptors[ref.Address]
	}
	stage.indexSets = g.indexSets
	if _, reindex := stmt.(*parser.Reindex); reindex {
		stage.indexRebuilds = make(map[ObjectAddress]*relationDescriptor, len(g.indexes))
		for _, ref := range g.indexes {
			stage.indexRebuilds[ref.Address] = g.descriptors[ref.Address]
			original := g.descriptors[ref.Address]
			if !original.sqlIndex.ready || !original.sqlIndex.valid || !original.sqlIndex.live {
				next, definition := *original, *original.sqlIndex
				definition.ready, definition.valid, definition.live = true, true, true
				next.sqlIndex = &definition
				if next.revision == DefinitionRevision(^uint64(0)) {
					e.mu.Unlock()
					return Result{}, fmt.Errorf("stuntgres: index revision exhausted")
				}
				next.revision++
				if err := stage.Replace(&next); err != nil {
					e.mu.Unlock()
					return Result{}, err
				}
			}
		}
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if _, drop := stmt.(*parser.DropIndex); drop {
		for _, ref := range g.indexes {
			if err := stage.Remove(ref); err != nil {
				return Result{}, err
			}
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(scope.op, prepared, PublicationToken{Epoch: auth.catalog.Epoch()})
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	for _, notice := range g.notices {
		scope.op.emitNotice(levelNotice, notice)
	}
	return Result{}, nil
}

// Stage observations are checked against the exact current catalog before any
// work is prepared. The publication loop also fences that catalog at install.
func (s *CatalogStage) validateIndexMaintenance(current *catalogState) error {
	if (len(s.indexParents) > 0 || len(s.indexRebuilds) > 0 || len(s.indexSets) > 0) && current.epoch != s.base.Epoch() {
		return ErrCatalogReplaced
	}
	for address, observed := range s.indexParents {
		if current.descriptors[address] != observed {
			return CatalogConflict{}
		}
	}
	for address, observed := range s.indexRebuilds {
		if observed == nil || observed.sqlIndex == nil || current.descriptors[address] != observed || s.base.state.descriptors[address] != observed {
			return CatalogConflict{}
		}
	}
	for parent, count := range s.indexSets {
		actual := 0
		for address, descriptor := range current.descriptors {
			d, ok := descriptor.(*relationDescriptor)
			if !ok || d.sqlIndex == nil || d.sqlIndex.parent != parent {
				continue
			}
			if s.indexRebuilds[address] == nil {
				return CatalogConflict{}
			}
			actual++
		}
		if actual != count {
			return CatalogConflict{}
		}
	}
	return nil
}
