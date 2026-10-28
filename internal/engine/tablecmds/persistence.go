package tablecmds

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/table"
)

// stageTablePersistence runs unlocked. The table AX grant protects ownership;
// each existing sequence additionally gets its own AX grant before generator
// capture. No physical-session cache is changed until actual sequence demand.
func stageTablePersistence(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage, next *catalog.Relation, destination string) (*catalog.Relation, error) {
	owned := []*catalog.Relation{next}
	if next.Sequence == nil {
		owned = nil
		for _, relation := range stage.StageRead().Relations() {
			if table.SequenceOwnedBy(relation, next.Address()) {
				owned = append(owned, relation)
			}
		}
		slices.SortFunc(owned, func(a, b *catalog.Relation) int {
			if a.Address().ObjectID < b.Address().ObjectID { return -1 }
			if a.Address().ObjectID > b.Address().ObjectID { return 1 }
			return 0
		})
	}
	for _, captured := range owned {
		address := captured.Address()
		if stage.Base.Get(address) != nil && address != next.Address() {
			if err := scope.AcquireSequence(e.Catalog.Domain.Ref(stage.Base.Epoch(), address), lock.AccessExclusive, false); err != nil { return nil, err }
		}
		e.Mu.Lock()
		err := func() error {
			if err := scope.Op.Context().Err(); err != nil { return err }
			fresh, err := e.AuthorizationLocked(scope.Op)
			if err != nil { return err }
			if fresh.Catalog.Epoch() != stage.Base.Epoch() { return ids.ErrCatalogReplaced }
			original := stage.Base.Get(address)
			if original != nil && fresh.Catalog.Get(address) != original { return catalog.Conflict{} }
			if address != next.Address() && !table.SequenceOwnedBy(captured, next.Address()) { return ids.ErrCatalogReplaced }
			generator := stage.Sequence.Generator(address)
			if generator == nil { generator = e.SequenceGeneratorLocked(scope, address) }
			if generator == nil { return ids.ErrCatalogReplaced }
			revision, err := e.Catalog.RevisionLocked()
			if err != nil { return err }
			changed := *captured
			options := *captured.Sequence
			options.Persistence = destination
			changed.Sequence = &options
			changed.ObjectRevision, changed.SequencePlanStamp = revision, revision
			if original == nil { stage.UpdateDescriptor(&changed) } else if err := stage.Replace(&changed); err != nil { return err }
			stage.SequenceState().CaptureIdentityOriginal(address, e.Catalog.State.Get(address))
			stage.SequenceState().SetPersistenceGenerator(address, sequence.ReplacementGenerator(*generator, revision, nil))
			return nil
		}()
		e.Mu.Unlock()
		if err != nil { return nil, err }
	}
	if next.Sequence != nil {
		return stage.Get(next.Address()).(*catalog.Relation), nil
	}
	changed := *next
	changed.Persistence = destination
	if err := stage.Replace(&changed); err != nil { return nil, err }
	return &changed, nil
}
