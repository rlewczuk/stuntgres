package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// authorizedStage returns stage, first beginning it over auth's catalog with
// auth's full authority when it is nil.
func (e *Engine) authorizedStage(stage *CatalogStage, auth AuthorizationView, s *session.Session) *CatalogStage {
	if stage == nil {
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.authorizeLocked(auth, s)
	}
	return stage
}

// mergeStageJournalLocked moves stage's descriptors and removals into changes
// after checking that each still has its original in the live catalog: base's
// descriptor, or an identity sequence's captured original. sequencesOnly
// merges only sequences and installs each merged sequence's generator;
// otherwise every staged generator of a kept object is installed. A changed
// original is a catalog.Conflict; changes may then be partly merged. The
// caller holds Engine.mu.
func (e *Engine) mergeStageJournalLocked(stage *CatalogStage, changes *session.Journal, base *catalog.State, sequencesOnly bool) error {
	selected := func(d catalog.Descriptor) bool {
		sequence, ok := d.(*catalog.Relation)
		return !sequencesOnly || ok && sequence.RelationKind == catalog.SequenceRelation
	}
	unchanged := func(address ids.ObjectAddress) bool {
		original := base.Get(address)
		if captured, identity := stage.sequence.IdentityOriginal(address); identity {
			original = captured
		}
		return e.catalog.State.Get(address) == original
	}
	for address, descriptor := range stage.Descriptors() {
		if !selected(descriptor) {
			continue
		}
		if !unchanged(address) {
			return catalog.Conflict{}
		}
		e.catalogChangeLocked(changes, address, descriptor)
		if sequencesOnly {
			changes.Generators[address] = stage.sequence.Generator(address)
		}
	}
	for address, removal := range stage.Removals() {
		if !selected(removal.Descriptor) {
			continue
		}
		if !unchanged(address) {
			return catalog.Conflict{}
		}
		e.catalogChangeLocked(changes, address, nil)
		delete(changes.Generators, address)
	}
	if !sequencesOnly {
		for address, generator := range stage.sequence.StagedGenerators() {
			if _, removed := stage.Removal(address); !removed {
				changes.Generators[address] = generator
			}
		}
	}
	return nil
}

// captureCheckSnapshots captures the comment and visibility snapshots that
// relation's CHECKs demand, under Engine.mu, which it takes.
func (e *Engine) captureCheckSnapshots(scope *StatementScope, relation *catalog.Relation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.captureCheckCommentSnapshotLocked(scope, relation); err != nil {
		return err
	}
	return e.captureCheckVisibilitySnapshotLocked(scope, relation)
}
