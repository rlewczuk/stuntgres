package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ScalarRoutinePublication is the completed, publication-only mapping proof.
// Its reads, descriptors and all returned backing are immutable and borrowed.
// Only RefreshStoredScalarRoutine can mint a nonempty proof.
type ScalarRoutinePublication struct {
	source, completed catalog.Read
	descriptors       []catalog.Descriptor
	before, after     *catalog.Procedure // Exact checked operation, never current lookup authority.
}

func (p *ScalarRoutinePublication) Descriptors() []catalog.Descriptor {
	if p == nil {
		return nil
	}
	return p.descriptors
}

// IndexSource authenticates an exact mapper output, never a same-address copy.
// Physical-root retention is separately owned by index and must authenticate
// the actual source root and row storage before consuming this proof.
func (p *ScalarRoutinePublication) IndexSource(index *catalog.Relation) (catalog.Read, catalog.Read, *catalog.Relation, error) {
	if p == nil || index == nil || index.SQLIndex == nil || p.source.State() == nil || p.completed.State() == nil || p.completed.Get(index.Address()) != index {
		return catalog.Read{}, catalog.Read{}, nil, ids.ErrCatalogReplaced
	}
	for _, descriptor := range p.descriptors {
		if descriptor != index {
			continue
		}
		old, ok := p.source.Relation(index.Address())
		if !ok || old.SQLIndex == nil {
			break
		}
		provider := p.source.Owner().Provider(index.Address().ClassID)
		if provider == nil {
			break
		}
		if err := provider.Validate(p.source, old); err != nil {
			return catalog.Read{}, catalog.Read{}, nil, err
		}
		if err := provider.Validate(p.completed, index); err != nil {
			return catalog.Read{}, catalog.Read{}, nil, err
		}
		return p.source, p.completed, old, nil
	}
	return catalog.Read{}, catalog.Read{}, nil, ids.ErrCatalogReplaced
}

// ScalarRelationSuccessor validates only the exact mapper-minted chain from an
// accepted relation to the current descriptor. It grants no executable refresh
// authority and does not validate unrelated analysis or folded providers.
func ScalarRelationSuccessor(read catalog.Read, original, current *catalog.Relation) bool {
	if original == nil || current == nil || read.Owner() == nil || original.Address() != current.Address() || read.Get(current.Address()) != current {
		return false
	}
	for current != original {
		publication, ok := current.ScalarPublication.(*ScalarRoutinePublication)
		if !ok || publication == nil || publication.source.Owner() != read.Owner() || publication.source.Epoch() != read.Epoch() ||
			publication.completed.Get(current.Address()) != current {
			return false
		}
		found := false
		for _, descriptor := range publication.descriptors {
			found = found || descriptor == current
		}
		if !found {
			return false
		}
		source, ok := publication.source.Relation(current.Address())
		if !ok || source == current || source.Revision() >= current.Revision() {
			return false
		}
		current = source
	}
	return true
}
