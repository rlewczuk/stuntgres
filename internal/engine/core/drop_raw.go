package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
)

// PrepareSelectedRawDrops captures the SAME raw removal worker and canonical
// index evolution. It does not execute dependencies or the later owned-sequence
// tail, mutate a candidate, or provide permission/label authority.
func (p *DropProvider) PrepareSelectedRawDrops() (*table.DropPreparation, error) {
	if p.Stage == nil || p.Stage.selectedDropsApplied {
		return nil, ids.ErrCatalogReplaced
	}
	roots := make([]ids.ObjectAddress, 0, len(p.Stage.selectedDropRoots))
	for _, root := range p.Stage.selectedDropRoots {
		roots = append(roots, root.reference.Address)
	}
	return p.prepareRawDropEffects(roots)
}

func (p *DropProvider) prepareRawDropEffects(roots []ids.ObjectAddress) (*table.DropPreparation, error) {
	p.Owner.Mu.Lock()
	defer p.Owner.Mu.Unlock()
	source := p.Owner.StageRead(p.Stage)
	prepared := &table.DropPreparation{Base: source, Discovery: source, Statement: p.Scope}
	if p.Scope != nil {
		prepared.TxRef = p.Scope.TxRef
	}
	raw := &DropProvider{Base: source, Owner: p.Owner, SearchPath: p.SearchPath, Prepared: prepared, Ctx: p.Ctx, Scope: p.Scope, dropRoles: p.Stage.DropRoles}
	for _, address := range roots {
		if address.SubID != 0 || source.Get(address) == nil {
			continue
		}
		ref := p.Owner.Catalog.Domain.Ref(source.Epoch(), address)
		if prepared.Ref == (ids.ObjectRef{}) {
			prepared.Ref = ref
		}
		if err := raw.removeObjectLocked(ref, address.ClassID == ids.NamespaceClassID); err != nil {
			return nil, err
		}
	}
	if err := index.EvolveIndexes(raw, ids.ObjectAddress{}, nil); err != nil {
		return nil, err
	}
	return prepared, nil
}
