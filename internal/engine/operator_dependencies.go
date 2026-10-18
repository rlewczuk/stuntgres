package engine

import "stuntgres/internal/engine/ids"

// Commutator and negator are reciprocal links, deliberately not dependencies.
func operatorDependencyAddresses(view CatalogRead, d *operatorDescriptor) []ids.ObjectAddress {
	refs := make([]ids.ObjectAddress, 0, 7)
	add := func(a ids.ObjectAddress) {
		if a.ObjectID == 0 {
			return
		}
		for _, old := range refs {
			if old == a {
				return
			}
		}
		refs = append(refs, a)
	}
	add(d.namespace)
	for _, id := range [...]ids.OID{d.left, d.right, d.result} {
		add(ids.ObjectAddress{ClassID: TypeClassID, ObjectID: id})
	}
	for _, id := range [...]ids.OID{d.function, d.restriction, d.join} {
		a := ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: id}
		if _, ok := view.state.descriptors[a]; ok {
			add(a)
		}
	}
	return refs
}
func (p *dropProvider) clearOperatorLinks(d *operatorDescriptor) error {
	for address, descriptor := range p.base.state.descriptors {
		peer, ok := descriptor.(*operatorDescriptor)
		if !ok || address == d.address || p.removed(address) {
			continue
		}
		if current, ok := p.replacement(address).(*operatorDescriptor); ok {
			peer = current
		}
		if peer.commutator != d.address.ObjectID && peer.negator != d.address.ObjectID {
			continue
		}
		next := *peer
		if next.commutator == d.address.ObjectID {
			next.commutator = 0
		}
		if next.negator == d.address.ObjectID {
			next.negator = 0
		}
		revision, err := p.revision(peer.revision)
		if err != nil {
			return err
		}
		next.revision = revision
		if err = p.replace(&next); err != nil {
			return err
		}
	}
	return nil
}
