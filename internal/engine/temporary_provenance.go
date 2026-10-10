package engine

import "stuntgres/internal/sqltype"

// Stored definitions retain their immutable reference manifests across native
// replacement. This is contextual authority, never general ObjectRef validity.
// Object addresses are nonreused; physical namespace tokens cannot be revived.
func storedReferenceEpochValid(view CatalogRead, consumer *relationDescriptor, ref ObjectRef) bool {
	if view.owner == nil || ref.owner != view.owner.owner || ref.Epoch == 0 || ref.Epoch > view.Epoch() {
		return false
	}
	if ref.Epoch == view.Epoch() {
		return true
	}
	address := ref.Address
	address.SubID = 0
	target := view.state.descriptors[address]
	if target == nil {
		return false
	}
	if consumer == nil {
		return false
	}
	namespace, ok := view.state.descriptors[consumer.namespace].(*namespaceDescriptor)
	if !ok || namespace.temporary == (SessionToken{}) {
		return false
	}
	switch target := target.(type) {
	case *relationDescriptor:
		ns, ok := view.state.descriptors[target.namespace].(*namespaceDescriptor)
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
		if ref.Address.ClassID != CollationClassID || ref.Address.SubID != 0 || namespace.temporary.owner != view.owner || view.state.descriptors[consumer.Address()] != consumer {
			return false
		}
		if capability, err := target.usage(); err != nil || !capability.Valid() {
			return false
		}
		if target.seeded() {
			return pgDependPinned(view, ref.Address) && (collationProvider{}).Validate(view, target) == nil
		}
		ns, ok := view.state.descriptors[target.Namespace()].(*namespaceDescriptor)
		return ok && ns.temporary == namespace.temporary && (collationProvider{}).Validate(view, target) == nil
	}
	return false
}

func storedSequenceEpochValid(view CatalogRead, consumer *relationDescriptor, ref ObjectRef, token SessionToken) bool {
	if ref.Epoch == view.Epoch() {
		return ref.owner == view.owner.owner
	}
	if !storedReferenceEpochValid(view, consumer, ref) {
		return false
	}
	return temporarySequenceEpochValid(view, ref, token)
}

func temporarySequenceEpochValid(view CatalogRead, ref ObjectRef, token SessionToken) bool {
	if token == (SessionToken{}) || ref.owner != view.owner.owner || ref.Epoch == 0 || ref.Epoch > view.Epoch() || ref.Address.SubID != 0 {
		return false
	}
	sequence, ok := view.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || sequence.relationKind != sequenceRelation {
		return false
	}
	ns, ok := view.state.descriptors[sequence.namespace].(*namespaceDescriptor)
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
func (r scalarRenderer) recordStoredType(view CatalogRead, ref ObjectRef) {
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

func storedCollationReferenceValid(view CatalogRead, consumer *relationDescriptor, ref ObjectRef, capability sqltype.CollationRef) bool {
	if ref.Address.ClassID != CollationClassID || ref.Address.SubID != 0 || !capability.Valid() || OID(capability.ID) != ref.Address.ObjectID || !storedReferenceEpochValid(view, consumer, ref) {
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
func (r scalarRenderer) recordStoredCollation(view CatalogRead, ref ObjectRef, capability sqltype.CollationRef) {
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
