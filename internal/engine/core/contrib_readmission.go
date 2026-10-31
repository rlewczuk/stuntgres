package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// Engine.mu is held; an admission wait never rebinds the lexical name to a
// replacement incarnation, even if the original descriptor was dropped.
func (s *StatementScope) pgrowlocksReadmitLocked(e *Engine, auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation) error {
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() ||
		auth.Catalog.Get(ref.Address) != descriptor || e.PrivateStorageLocked(s.PendingCatalog(), ref.Address) != stored {
		return ids.ErrCatalogReplaced
	}
	current, err := resolveRelationSnapshot(auth, name, true)
	if err != nil {
		return err
	}
	if current != ref {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// Engine.mu is held. Waiting never rebinds a captured OID to a new incarnation.
func (s *StatementScope) verifyHeapamReadmitLocked(e *Engine, auth AuthorizationView, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation, sequence *storage.SequenceGenerator) error {
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || auth.Catalog.Get(ref.Address) != descriptor || e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) != stored {
		return ids.ErrCatalogReplaced
	}
	if descriptor.RelationKind == catalog.SequenceRelation && e.SequenceGeneratorLocked(s, ref.Address) != sequence {
		return ids.ErrCatalogReplaced
	}
	return nil
}
