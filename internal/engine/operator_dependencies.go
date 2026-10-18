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
	for _, id := range [...]ids.OID{d.Left, d.Right, d.Result} {
		add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
	}
	for _, id := range [...]ids.OID{d.Function, d.Restriction, d.Join} {
		a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
		if _, ok := view.Find(a); ok {
			add(a)
		}
	}
	return refs
}
func (p *dropProvider) clearOperatorLinks(d *operatorDescriptor) error {
	for address, descriptor := range p.base.Descriptors() {
		peer, ok := descriptor.(*operatorDescriptor)
		if !ok || address == d.address || p.removed(address) {
			continue
		}
		if current, ok := p.replacement(address).(*operatorDescriptor); ok {
			peer = current
		}
		if peer.Commutator != d.address.ObjectID && peer.Negator != d.address.ObjectID {
			continue
		}
		next := *peer
		if next.Commutator == d.address.ObjectID {
			next.Commutator = 0
		}
		if next.Negator == d.address.ObjectID {
			next.Negator = 0
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
