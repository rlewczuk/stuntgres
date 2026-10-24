package engine

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
)

func (e *Engine) serializableEligibleLocked(ref ids.ObjectRef, changes *session.Journal) bool {
	d, ok := e.privateCatalogLocked(changes).Relation(ref.Address)
	if !ok || d.RelationKind != catalog.TableRelation {
		return false
	}
	ns, ok := e.privateCatalogLocked(changes).Namespace(d.Namespace())
	return ok && ns.Temporary == (ids.SessionToken{}) && d.Namespace().ObjectID != ids.PGCatalogNamespaceOID && e.storage.Relations[ref.Address] != nil
}

func (e *Engine) serializableReadLocked(s *StatementScope, ref ids.ObjectRef) error {
	if ref.Domain() != e.catalog.Domain || ref.Address.SubID != 0 {
		return errors.New("stuntgres: foreign SSI relation")
	}
	if ref.Epoch != e.catalog.State.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if s.txRef.Domain() != e.catalog.Domain || session.TxOwnerOf(s.txRef) != s.owner || s.txRef != s.owner.Ref {
		return errors.New("stuntgres: invalid SSI read authority")
	}
	// Relevance is the cheap test; most readers are not serializable.
	node := e.visibility.RelevantSerializableLocked(s.txRef)
	if node == nil || !e.serializableEligibleLocked(ref, s.owner.Catalog) {
		return nil
	}
	return e.visibility.SerializableReadLocked(node, ref.Address)
}

func (e *Engine) serializableWriteLocked(s *StatementScope, ref ids.ObjectRef, events []visibility.SerializableWrite) error {
	if ref.Domain() != e.catalog.Domain || ref.Address.SubID != 0 || s.txRef.Domain() != e.catalog.Domain || session.TxOwnerOf(s.txRef) != s.owner || s.txRef != s.owner.Ref {
		return errors.New("stuntgres: invalid SSI write authority")
	}
	if ref.Epoch != e.catalog.State.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, event := range events {
		if !event.Rewrite && (event.Row == 0 || event.Version == 0 || event.Deletion && event.Base != event.Version) {
			return errors.New("stuntgres: invalid SSI write identity")
		}
	}
	if !e.serializableEligibleLocked(ref, s.owner.Catalog) {
		return nil
	}
	node := e.visibility.RelevantSerializableLocked(s.txRef)
	if node == nil {
		return nil
	}
	return e.visibility.SerializableWriteLocked(node, s.txRef, ref.Address, events)
}

// Rollback restores surviving identity evidence only, never graph edges,
// predicates, doom or attempted-write history. Engine.mu and owner.mu held.
func (e *Engine) reconcileSerializableWritesLocked(t *session.TransactionOwner) {
	e.visibility.ReconcileSerializableWritesLocked(t.Ref, t.SerializableWriteSurvives)
}
