package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeIndexMaintenance(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return fail(err)
	}
	g := scope.indexesOrZero().maintenance
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
			scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`table "%s" has no indexes to reindex`, g.descriptors[g.targets[0].Address].Name())})
		}
		for _, notice := range g.notices {
			scope.op.emitNotice(levelNotice, notice)
		}
		return Result{}, nil
	}
	if s, ok := stmt.(*parser.AlterIndex); ok && s.Action == "rename" {
		return e.renameRelationLocked(scope, scope.op, auth, s.Name, "", s.Target.Text, s.IfExists, false, false, false, count)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.session)
	stage.indexOutput = scope.output
	stage.indexParents = make(map[ids.ObjectAddress]*catalog.Relation, len(g.parents))
	for _, ref := range g.parents {
		stage.indexParents[ref.Address] = g.descriptors[ref.Address]
		stage.domainBases = append(stage.domainBases, g.descriptors[ref.Address])
	}
	stage.indexSets = g.indexSets
	for address, observed := range g.descriptors {
		if observed.SQLIndex != nil {
			stage.domainBases = append(stage.domainBases, g.descriptors[address])
		}
	}
	if _, reindex := stmt.(*parser.Reindex); reindex {
		stage.indexRebuilds = make(map[ids.ObjectAddress]*catalog.Relation, len(g.indexes))
		for _, ref := range g.indexes {
			stage.indexRebuilds[ref.Address] = g.descriptors[ref.Address]
			original := g.descriptors[ref.Address]
			if !original.SQLIndex.Ready || !original.SQLIndex.Valid || !original.SQLIndex.Live {
				next, definition := *original, *original.SQLIndex
				definition.Ready, definition.Valid, definition.Live = true, true, true
				next.SQLIndex = &definition
				if next.Revision() == ids.DefinitionRevision(^uint64(0)) {
					e.mu.Unlock()
					return Result{}, fmt.Errorf("stuntgres: index revision exhausted")
				}
				next.ObjectRevision++
				if err := stage.Replace(&next); err != nil {
					e.mu.Unlock()
					return Result{}, err
				}
			}
		}
	}
	if s, ok := stmt.(*parser.AlterIndex); ok && s.Action == "attach" {
		parent := g.descriptors[g.targets[0].Address]
		child := g.descriptors[g.attach.Address]
		if err := e.stagePartitionIndexAttach(scope.op, stage, parent, child); err != nil {
			e.mu.Unlock()
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if drop, ok := stmt.(*parser.DropIndex); ok {
		for _, ref := range g.indexes {
			if err := e.stagePartitionIndexDrop(stage, g.descriptors[ref.Address]); err != nil {
				return Result{}, err
			}
		}
		roots := make([]ids.ObjectAddress, len(g.indexes))
		for i, ref := range g.indexes {
			roots[i] = ref.Address
		}
		stage.dependencyPath, _ = e.referencePath(auth)
		if err := scope.stageExpressionDependents(stage, roots, drop.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	if _, ok := stmt.(*parser.DropIndex); ok {
		if err := e.stagePartitionIndexValidity(stage, ids.ObjectAddress{}); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	if scope.partitionIndexTransaction(stmt) {
		err := e.stageCatalogOverlay(scope, stage, cloneCatalogChanges(scope.owner.catalog))
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
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
func (s *CatalogStage) validateIndexMaintenance(current *catalog.State) error {
	if (len(s.indexParents) > 0 || len(s.indexRebuilds) > 0 || len(s.indexSets) > 0) && current.Epoch() != s.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for address, observed := range s.indexParents {
		if current.Get(address) != observed {
			return catalog.Conflict{}
		}
	}
	for address, observed := range s.indexRebuilds {
		if observed == nil || observed.SQLIndex == nil || current.Get(address) != observed || s.Base.Get(address) != observed {
			return catalog.Conflict{}
		}
	}
	for parent, count := range s.indexSets {
		actual := 0
		for address, descriptor := range current.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || d.SQLIndex == nil || d.SQLIndex.Parent != parent {
				continue
			}
			// Replacement observes the existing complete index set; it does
			// not request REINDEX or replace any index descriptor.
			replacement := s.materializedReplacement
			if s.indexRebuilds[address] == nil && (replacement == nil || replacement.original.Address() != parent || s.Base.Get(address) != d) {
				return catalog.Conflict{}
			}
			actual++
		}
		if actual != count {
			return catalog.Conflict{}
		}
	}
	return nil
}

// indexScope is the statement's index slot: the index maintenance grant,
// index outputs retained for probes and the exclusion-constraint projection.
type indexScope struct {
	maintenance      *indexMaintenanceGrant
	existingOutputs  map[ids.ObjectAddress]map[indexRowIdentity]storage.IndexOutput
	exclusionModes   exclusionModes
	exclusionStarted bool
}

// indexes returns the statement's indexes slot, allocating it on first use.
func (s *StatementScope) indexes() *indexScope {
	if s.indexesSlot == nil {
		s.indexesSlot = new(indexScope)
	}
	return s.indexesSlot
}

// indexesOrZero reads the indexes slot; a nil slot reads as zero.
func (s *StatementScope) indexesOrZero() indexScope {
	if s.indexesSlot == nil {
		return indexScope{}
	}
	return *s.indexesSlot
}

// endStatement drops the index outputs retained for probes and the index
// maintenance grant. Exclusion modes stay as the statement left them.
func (x *indexScope) endStatement() {
	if x == nil {
		return
	}
	x.existingOutputs, x.maintenance = nil, nil
}
