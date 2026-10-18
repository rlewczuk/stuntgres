package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Stored definitions retain their immutable reference manifests across native
// replacement. This is contextual authority, never general ObjectRef validity.
// Object addresses are nonreused; physical namespace tokens cannot be revived.
func storedReferenceEpochValid(view CatalogRead, consumer *relationDescriptor, ref ids.ObjectRef) bool {
	if view.owner == nil || ref.Domain() != view.owner.domain || ref.Epoch == 0 || ref.Epoch > view.Epoch() {
		return false
	}
	if ref.Epoch == view.Epoch() {
		return true
	}
	address := ref.Address
	address.SubID = 0
	target := view.Get(address)
	if target == nil {
		return false
	}
	if consumer == nil {
		return false
	}
	namespace, ok := view.Namespace(consumer.namespace)
	if !ok || namespace.temporary == (ids.SessionToken{}) {
		return false
	}
	switch target := target.(type) {
	case *relationDescriptor:
		ns, ok := view.Namespace(target.namespace)
		if !ok || ns.temporary != namespace.temporary {
			return false
		}
		return ref.Address.SubID == 0 || target.hasAttribute(ref.Address.SubID)
	case *typeDescriptor:
		// Full rendering has already compared each concrete node's exact Usage
		// generation. Native replacement retains these builtin descriptors, not
		// user-defined/domain generations under this exception.
		return ref.Address.SubID == 0 && !target.typ.IsUserDefined() && !target.typ.IsDomain() && !(target.typ.IsArray() && target.typ.Element().IsDomain())
	case *collationDescriptor:
		if ref.Address.ClassID != ids.CollationClassID || ref.Address.SubID != 0 || namespace.temporary.Domain() != view.Domain() || view.Get(consumer.Address()) != consumer {
			return false
		}
		if capability, err := target.usage(); err != nil || !capability.Valid() {
			return false
		}
		if target.seeded() {
			return pgDependPinned(view, ref.Address) && (collationProvider{}).Validate(view, target) == nil
		}
		ns, ok := view.Namespace(target.Namespace())
		return ok && ns.temporary == namespace.temporary && (collationProvider{}).Validate(view, target) == nil
	case *procedureDescriptor:
		if ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || namespace.temporary.Domain() != view.Domain() || view.Get(consumer.Address()) != consumer {
			return false
		}
		if target.builtin != nil {
			// Only the genuine immutable release declaration can bridge a
			// retained TEMP recipe; ACL authority is recaptured separately.
			return pgDependPinned(view, ref.Address) && (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, target) == nil
		}
		ns, ok := view.Namespace(target.namespace)
		return ok && ns.temporary == namespace.temporary && (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, target) == nil
	}
	return false
}

// Old epochs belong only to the immutable manifest of this exact retained view.
// The ordinary ObjectRef validator and every unrelated statement stay strict.
func storedViewReferenceValid(catalog CatalogRead, consumer *relationDescriptor, ref ids.ObjectRef) bool {
	if catalog.owner == nil || ref.Domain() != catalog.owner.domain || ref.Epoch == 0 || ref.Epoch > catalog.Epoch() {
		return false
	}
	address := ref.Address
	address.SubID = 0
	target := catalog.Get(address)
	if target == nil {
		return false
	}
	if ref.Epoch == catalog.Epoch() {
		return true
	}
	if consumer == nil || consumer.View == nil {
		return false
	}
	owner, temporary := consumer.View.TemporaryConsumer()
	if owner != consumer.address || catalog.Get(consumer.address) != consumer {
		return false
	}
	namespace, ok := catalog.Namespace(consumer.namespace)
	if !ok || namespace.temporary == (ids.SessionToken{}) || namespace.temporary != temporary || namespace.temporary.Domain() != catalog.Domain() {
		return false
	}
	captured, exists := consumer.View.CapturedReference(ref)
	if !exists || captured == nil {
		return false
	}
	if relation, ok := target.(*relationDescriptor); ok && relation.RelationKind == catalogRelation {
		return target == captured && pgDependPinned(catalog, address) && (ref.Address.SubID == 0 || relation.hasAttribute(ref.Address.SubID))
	}
	if !storedReferenceEpochValid(catalog, consumer, ref) {
		return false
	}
	switch actual := target.(type) {
	case *relationDescriptor:
		old, ok := captured.(*relationDescriptor)
		return ok && actual.namespace == old.namespace && actual.RelationKind == old.RelationKind
	case *typeDescriptor:
		old, ok := captured.(*typeDescriptor)
		return ok && actual.typ == old.typ
	case *collationDescriptor:
		old, ok := captured.(*collationDescriptor)
		if !ok {
			return false
		}
		before, beforeErr := old.usage()
		after, afterErr := actual.usage()
		return beforeErr == nil && afterErr == nil && before == after
	case *procedureDescriptor:
		old, ok := captured.(*procedureDescriptor)
		// An old-epoch intagg occurrence bridges replacement only through the
		// exact descriptor captured by this retained TEMP consumer. Ordinary
		// current-epoch COW declarations take the path above, not this bridge.
		if ok && (old.intaggProvider() != nil || actual.intaggProvider() != nil) && actual != old {
			return false
		}
		return ok && old.address == address && old.revision != 0 &&
			old.owner.ID() != 0 && old.owner == catalog.owner.principals.Principal(old.owner.ID()) &&
			sameRoutineDeclaration(actual, old)
	}
	return false
}

func storedViewUseReference(catalog CatalogRead, consumer *relationDescriptor, ref ids.ObjectRef) (ids.ObjectRef, error) {
	if !storedViewReferenceValid(catalog, consumer, ref) {
		return ids.ObjectRef{}, ids.ErrCatalogReplaced
	}
	ref.Epoch = catalog.Epoch()
	return ref, nil
}

func storedSequenceEpochValid(view CatalogRead, consumer *relationDescriptor, ref ids.ObjectRef, token ids.SessionToken) bool {
	if ref.Epoch == view.Epoch() {
		return ref.Domain() == view.owner.domain
	}
	if !storedReferenceEpochValid(view, consumer, ref) {
		return false
	}
	return temporarySequenceEpochValid(view, ref, token)
}

func temporarySequenceEpochValid(view CatalogRead, ref ids.ObjectRef, token ids.SessionToken) bool {
	if token == (ids.SessionToken{}) || ref.Domain() != view.owner.domain || ref.Epoch == 0 || ref.Epoch > view.Epoch() || ref.Address.SubID != 0 {
		return false
	}
	sequence, ok := view.Relation(ref.Address)
	if !ok || sequence.RelationKind != sequenceRelation {
		return false
	}
	ns, ok := view.Namespace(sequence.namespace)
	return ok && ns.temporary == token
}

func (r scalarRenderer) retainedConsumer() *relationDescriptor {
	if r.owner != nil {
		return r.owner
	}
	return r.provenance
}

// Type refs have no node-local captured ObjectRef. Choose the already recorded
// immutable manifest ref only after sql has checked exact builtin Type/Usage.
func (r scalarRenderer) recordStoredType(view CatalogRead, ref ids.ObjectRef) {
	for _, old := range r.capturedManifest {
		if old.Address == ref.Address && storedReferenceEpochValid(view, r.retainedConsumer(), old) {
			r.record(old)
			return
		}
	}
	for _, old := range r.capturedChecks {
		if old.ref.Address == ref.Address && storedReferenceEpochValid(view, r.retainedConsumer(), old.ref) {
			r.record(old.ref)
			return
		}
	}
	r.record(ref)
}

func storedCollationReferenceValid(view CatalogRead, consumer *relationDescriptor, ref ids.ObjectRef, capability sqltype.CollationRef) bool {
	if ref.Address.ClassID != ids.CollationClassID || ref.Address.SubID != 0 || !capability.Valid() || ids.OID(capability.ID) != ref.Address.ObjectID || !storedReferenceEpochValid(view, consumer, ref) {
		return false
	}
	target, ok := view.Descriptor(ref.Address)
	if !ok {
		return false
	}
	collation, ok := target.(*collationDescriptor)
	if !ok {
		return false
	}
	actual, err := collation.usage()
	return err == nil && actual == capability
}

// The concrete node Usage has already been checked against the live provider.
// Retained definitions must serialize the same lawful captured manifest epoch,
// rather than manufacture a fresh reference that disagrees with that manifest.
func (r scalarRenderer) recordStoredCollation(view CatalogRead, ref ids.ObjectRef, capability sqltype.CollationRef) {
	for _, old := range r.capturedManifest {
		if old.Address == ref.Address && storedCollationReferenceValid(view, r.retainedConsumer(), old, capability) {
			r.record(old)
			return
		}
	}
	for _, old := range r.capturedChecks {
		if old.ref.Address == ref.Address && storedCollationReferenceValid(view, r.retainedConsumer(), old.ref, capability) {
			r.record(old.ref)
			return
		}
	}
	r.record(ref)
}
