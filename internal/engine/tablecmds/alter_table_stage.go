package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// authorizedStage returns stage, first beginning it over auth's catalog with
// auth's full authority when it is nil.
func authorizedStage(e *core.Engine, stage *core.CatalogStage, auth core.AuthorizationView, s *session.Session) *core.CatalogStage {
	if stage == nil {
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, s)
	}
	return stage
}

// validateAlterSequenceOriginalsLocked checks the sequence changes admitted by
// this stage against their grant-time committed originals. It transfers no
// effects: ordinary metadata transfer consumes descriptors, generators and
// removals together before physical recipes run. The caller holds Engine.Mu.
func validateAlterSequenceOriginalsLocked(e *core.Engine, stage *core.CatalogStage, base *catalog.State) error {
	unchanged := func(address ids.ObjectAddress) bool {
		original := base.Get(address)
		if captured, identity := stage.Sequence.IdentityOriginal(address); identity {
			original = captured
		}
		return e.Catalog.State.Get(address) == original
	}
	for address, descriptor := range stage.Descriptors() {
		if sequence, ok := descriptor.(*catalog.Relation); ok && sequence.RelationKind == catalog.SequenceRelation && !unchanged(address) {
			return catalog.Conflict{}
		}
	}
	for address, removal := range stage.Removals() {
		if sequence, ok := removal.Descriptor.(*catalog.Relation); ok && sequence.RelationKind == catalog.SequenceRelation && !unchanged(address) {
			return catalog.Conflict{}
		}
	}
	return nil
}

// CaptureCheckSnapshots captures the comment and visibility snapshots that
// relation's CHECKs demand, under Engine.mu, which it takes.
func CaptureCheckSnapshots(e *core.Engine, scope *core.StatementScope, relation *catalog.Relation) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := e.CaptureCheckCommentSnapshotLocked(scope, relation); err != nil {
		return err
	}
	return e.CaptureCheckVisibilitySnapshotLocked(scope, relation)
}
