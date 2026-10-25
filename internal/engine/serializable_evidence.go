package engine

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
)

func (e *Engine) serializableEligibleLocked(ref ids.ObjectRef, changes *session.Journal) bool {
	d, ok := e.PrivateCatalogLocked(changes).Relation(ref.Address)
	if !ok || d.RelationKind != catalog.TableRelation {
		return false
	}
	ns, ok := e.PrivateCatalogLocked(changes).Namespace(d.Namespace())
	return ok && ns.Temporary == (ids.SessionToken{}) && d.Namespace().ObjectID != ids.PGCatalogNamespaceOID && e.Storage.Relations[ref.Address] != nil
}

func (e *Engine) SerializableReadLocked(s *StatementScope, ref ids.ObjectRef) error {
	if ref.Domain() != e.Catalog.Domain || ref.Address.SubID != 0 {
		return errors.New("stuntgres: foreign SSI relation")
	}
	if ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if s.TxRef.Domain() != e.Catalog.Domain || session.TxOwnerOf(s.TxRef) != s.Owner || s.TxRef != s.Owner.Ref {
		return errors.New("stuntgres: invalid SSI read authority")
	}
	// Relevance is the cheap test; most readers are not serializable.
	node := e.Visibility.RelevantSerializableLocked(s.TxRef)
	if node == nil || !e.serializableEligibleLocked(ref, s.Owner.Catalog) {
		return nil
	}
	return e.Visibility.SerializableReadLocked(node, ref.Address)
}

func (e *Engine) SerializableWriteLocked(s *StatementScope, ref ids.ObjectRef, events []visibility.SerializableWrite) error {
	if ref.Domain() != e.Catalog.Domain || ref.Address.SubID != 0 || s.TxRef.Domain() != e.Catalog.Domain || session.TxOwnerOf(s.TxRef) != s.Owner || s.TxRef != s.Owner.Ref {
		return errors.New("stuntgres: invalid SSI write authority")
	}
	if ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, event := range events {
		if !event.Rewrite && (event.Row == 0 || event.Version == 0 || event.Deletion && event.Base != event.Version) {
			return errors.New("stuntgres: invalid SSI write identity")
		}
	}
	if !e.serializableEligibleLocked(ref, s.Owner.Catalog) {
		return nil
	}
	node := e.Visibility.RelevantSerializableLocked(s.TxRef)
	if node == nil {
		return nil
	}
	return e.Visibility.SerializableWriteLocked(node, s.TxRef, ref.Address, events)
}

// Rollback restores surviving identity evidence only, never graph edges,
// predicates, doom or attempted-write history. Engine.mu and owner.mu held.
func (e *Engine) reconcileSerializableWritesLocked(t *session.TransactionOwner) {
	e.Visibility.ReconcileSerializableWritesLocked(t.Ref, t.SerializableWriteSurvives)
}
