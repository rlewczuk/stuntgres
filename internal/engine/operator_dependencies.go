package engine

import (
	"stuntgres/internal/engine/catalog"
)

func (p *dropProvider) clearOperatorLinks(d *catalog.Operator) error {
	for address, descriptor := range p.base.Descriptors() {
		peer, ok := descriptor.(*catalog.Operator)
		if !ok || address == d.Address() || p.removed(address) {
			continue
		}
		if current, ok := p.replacement(address).(*catalog.Operator); ok {
			peer = current
		}
		if peer.Commutator != d.Address().ObjectID && peer.Negator != d.Address().ObjectID {
			continue
		}
		next := *peer
		if next.Commutator == d.Address().ObjectID {
			next.Commutator = 0
		}
		if next.Negator == d.Address().ObjectID {
			next.Negator = 0
		}
		revision, err := p.revision(peer.Revision())
		if err != nil {
			return err
		}
		next.ObjectRevision = revision
		if err = p.replace(&next); err != nil {
			return err
		}
	}
	return nil
}
