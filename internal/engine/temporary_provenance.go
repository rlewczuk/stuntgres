package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Stored definitions retain their immutable reference manifests across native
// replacement. This is contextual authority, never general ObjectRef validity.
// Object addresses are nonreused; physical namespace tokens cannot be revived.
func storedReferenceEpochValid(view CatalogRead, consumer *catalog.Relation, ref ids.ObjectRef) bool {
	if view.Owner() == nil || ref.Domain() != view.Owner().Domain || ref.Epoch == 0 || ref.Epoch > view.Epoch() {
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
	namespace, ok := view.Namespace(consumer.Namespace())
	if !ok || namespace.Temporary == (ids.SessionToken{}) {
		return false
	}
	switch target := target.(type) {
	case *catalog.Relation:
		ns, ok := view.Namespace(target.Namespace())
		if !ok || ns.Temporary != namespace.Temporary {
			return false
		}
		return ref.Address.SubID == 0 || target.HasAttribute(ref.Address.SubID)
	case *typeDescriptor:
		// Full rendering has already compared each concrete node's exact Usage
		// generation. Native replacement retains these builtin descriptors, not
		// user-defined/domain generations under this exception.
		return ref.Address.SubID == 0 && !target.SQLType.IsUserDefined() && !target.SQLType.IsDomain() && !(target.SQLType.IsArray() && target.SQLType.Element().IsDomain())
	case *collationDescriptor:
		if ref.Address.ClassID != ids.CollationClassID || ref.Address.SubID != 0 || namespace.Temporary.Domain() != view.Domain() || view.Get(consumer.Address()) != consumer {
			return false
		}
		if capability, err := target.Usage(); err != nil || !capability.Valid() {
			return false
		}
		if target.Seeded() {
			return pgDependPinned(view, ref.Address) && (collationProvider{}).Validate(view, target) == nil
		}
		ns, ok := view.Namespace(target.Namespace())
		return ok && ns.Temporary == namespace.Temporary && (collationProvider{}).Validate(view, target) == nil
	case *catalog.Procedure:
		if ref.Address.ClassID != ids.ProcedureClassID || ref.Address.SubID != 0 || namespace.Temporary.Domain() != view.Domain() || view.Get(consumer.Address()) != consumer {
			return false
		}
		if target.Builtin != nil {
			// Only the genuine immutable release declaration can bridge a
			// retained TEMP recipe; ACL authority is recaptured separately.
			return pgDependPinned(view, ref.Address) && (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, target) == nil
		}
		ns, ok := view.Namespace(target.Namespace())
		return ok && ns.Temporary == namespace.Temporary && (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, target) == nil
	}
	return false
}

// Old epochs belong only to the immutable manifest of this exact retained view.
// The ordinary ObjectRef validator and every unrelated statement stay strict.
func storedViewReferenceValid(view CatalogRead, consumer *catalog.Relation, ref ids.ObjectRef) bool {
	if view.Owner() == nil || ref.Domain() != view.Owner().Domain || ref.Epoch == 0 || ref.Epoch > view.Epoch() {
		return false
	}
	address := ref.Address
	address.SubID = 0
	target := view.Get(address)
	if target == nil {
		return false
	}
	if ref.Epoch == view.Epoch() {
		return true
	}
	if consumer == nil || consumer.View == nil {
		return false
	}
	owner, temporary := consumer.View.TemporaryConsumer()
	if owner != consumer.Address() || view.Get(consumer.Address()) != consumer {
		return false
	}
	namespace, ok := view.Namespace(consumer.Namespace())
	if !ok || namespace.Temporary == (ids.SessionToken{}) || namespace.Temporary != temporary || namespace.Temporary.Domain() != view.Domain() {
		return false
	}
	captured, exists := consumer.View.CapturedReference(ref)
	if !exists || captured == nil {
		return false
	}
	if relation, ok := target.(*catalog.Relation); ok && relation.RelationKind == catalog.CatalogRelation {
		return target == captured && pgDependPinned(view, address) && (ref.Address.SubID == 0 || relation.HasAttribute(ref.Address.SubID))
	}
	if !storedReferenceEpochValid(view, consumer, ref) {
		return false
	}
	switch actual := target.(type) {
	case *catalog.Relation:
		old, ok := captured.(*catalog.Relation)
		return ok && actual.Namespace() == old.Namespace() && actual.RelationKind == old.RelationKind
	case *typeDescriptor:
		old, ok := captured.(*typeDescriptor)
		return ok && actual.SQLType == old.SQLType
	case *collationDescriptor:
		old, ok := captured.(*collationDescriptor)
		if !ok {
			return false
		}
		before, beforeErr := old.Usage()
		after, afterErr := actual.Usage()
		return beforeErr == nil && afterErr == nil && before == after
	case *catalog.Procedure:
		old, ok := captured.(*catalog.Procedure)
		// An old-epoch intagg occurrence bridges replacement only through the
		// exact descriptor captured by this retained TEMP consumer. Ordinary
		// current-epoch COW declarations take the path above, not this bridge.
		if ok && (intaggProviderOf(old) != nil || intaggProviderOf(actual) != nil) && actual != old {
			return false
		}
		return ok && old.Address() == address && old.Revision() != 0 &&
			old.Owner.ID() != 0 && old.Owner == view.Owner().Principals.Principal(old.Owner.ID()) &&
			sameRoutineDeclaration(actual, old)
	}
	return false
}

func storedViewUseReference(catalog CatalogRead, consumer *catalog.Relation, ref ids.ObjectRef) (ids.ObjectRef, error) {
	if !storedViewReferenceValid(catalog, consumer, ref) {
		return ids.ObjectRef{}, ids.ErrCatalogReplaced
	}
	ref.Epoch = catalog.Epoch()
	return ref, nil
}

func storedSequenceEpochValid(view CatalogRead, consumer *catalog.Relation, ref ids.ObjectRef, token ids.SessionToken) bool {
	if ref.Epoch == view.Epoch() {
		return ref.Domain() == view.Owner().Domain
	}
	if !storedReferenceEpochValid(view, consumer, ref) {
		return false
	}
	return temporarySequenceEpochValid(view, ref, token)
}

func temporarySequenceEpochValid(view CatalogRead, ref ids.ObjectRef, token ids.SessionToken) bool {
	if token == (ids.SessionToken{}) || ref.Domain() != view.Owner().Domain || ref.Epoch == 0 || ref.Epoch > view.Epoch() || ref.Address.SubID != 0 {
		return false
	}
	sequence, ok := view.Relation(ref.Address)
	if !ok || sequence.RelationKind != catalog.SequenceRelation {
		return false
	}
	ns, ok := view.Namespace(sequence.Namespace())
	return ok && ns.Temporary == token
}

func (r scalarRenderer) retainedConsumer() *catalog.Relation {
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
		if old.Ref.Address == ref.Address && storedReferenceEpochValid(view, r.retainedConsumer(), old.Ref) {
			r.record(old.Ref)
			return
		}
	}
	r.record(ref)
}

func storedCollationReferenceValid(view CatalogRead, consumer *catalog.Relation, ref ids.ObjectRef, capability sqltype.CollationRef) bool {
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
	actual, err := collation.Usage()
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
		if old.Ref.Address == ref.Address && storedCollationReferenceValid(view, r.retainedConsumer(), old.Ref, capability) {
			r.record(old.Ref)
			return
		}
	}
	r.record(ref)
}
