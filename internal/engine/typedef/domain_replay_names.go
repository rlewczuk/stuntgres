package typedef

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Replay names are transient rendering predictions keyed by captured identity,
// not a catalog or destination OID allocator.
type DomainReplayNames struct {
	occupied map[ids.NameKey]sqltype.ID
	Names    map[sqltype.ID]ids.NameKey
	Arrays   map[sqltype.ID]sqltype.ID
	elements map[sqltype.ID]sqltype.ID
}

func NewDomainReplayNames() *DomainReplayNames {
	r := &DomainReplayNames{
		occupied: make(map[ids.NameKey]sqltype.ID), Names: make(map[sqltype.ID]ids.NameKey),
		Arrays: make(map[sqltype.ID]sqltype.ID), elements: make(map[sqltype.ID]sqltype.ID),
	}
	for _, typ := range sqltype.Builtins() {
		namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		if typ.NamespaceName() == "information_schema" {
			namespace.ObjectID = 13229
		}
		key := ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: typ.Name()}
		r.occupied[key], r.Names[typ.ID()] = typ.ID(), key
		r.Arrays[typ.ID()] = typ.AssociatedArray()
		if typ.IsArray() {
			r.elements[typ.ID()] = typ.Element().ID()
		}
	}
	return r
}

func (r *DomainReplayNames) arrayName(key ids.NameKey) ids.NameKey {
	key.Name = ChooseAssociatedArrayName(key.Name, func(candidate string) bool {
		key.Name = candidate
		return r.occupied[key] != 0
	})
	return key
}

func (r *DomainReplayNames) Reserve(d *catalog.Type) error {
	return r.ReserveIdentity(d.SQLType.ID(), catalog.DescriptorNameKey(d))
}

func (r *DomainReplayNames) ReserveIdentity(id sqltype.ID, key ids.NameKey) error {
	if incumbent := r.occupied[key]; incumbent != 0 {
		element := r.elements[incumbent]
		if element == 0 || r.Arrays[element] != incumbent {
			return fmt.Errorf("stuntgres: conflicting domain replay type name")
		}
		moved := r.arrayName(key)
		delete(r.occupied, key)
		r.occupied[moved], r.Names[incumbent] = incumbent, moved
	}
	r.occupied[key], r.Names[id] = id, key
	return nil
}

func (r *DomainReplayNames) Associate(d *catalog.Type) {
	if d.Array == 0 {
		return
	}
	r.AssociateIdentity(d.SQLType.ID(), d.Array)
}

func (r *DomainReplayNames) AssociateIdentity(id, array sqltype.ID) {
	key := r.arrayName(r.Names[id])
	r.occupied[key], r.Names[array] = array, key
	r.Arrays[id], r.elements[array] = array, id
}

// Relocate predicts ordinary ALTER TYPE, including reciprocal generated-array
// displacement. Schema moves preserve companion spelling; renames regenerate it.
func (r *DomainReplayNames) Relocate(id sqltype.ID, key ids.NameKey, moving bool) error {
	old, ok := r.Names[id]
	if !ok {
		return fmt.Errorf("stuntgres: missing replay identity")
	}
	array := r.Arrays[id]
	oldArray := r.Names[array]
	if moving {
		arrayKey := oldArray
		arrayKey.Namespace = key.Namespace
		if r.occupied[key] != 0 || array != 0 && r.occupied[arrayKey] != 0 {
			return fmt.Errorf("stuntgres: conflicting extension replay destination")
		}
		delete(r.occupied, old)
		r.occupied[key], r.Names[id] = id, key
		if array != 0 {
			delete(r.occupied, oldArray)
			r.occupied[arrayKey], r.Names[array] = array, arrayKey
		}
		return nil
	}
	if err := r.ReserveIdentity(id, key); err != nil {
		return err
	}
	delete(r.occupied, old)
	if array != 0 {
		delete(r.occupied, r.Names[array])
		r.AssociateIdentity(id, array)
	}
	return nil
}

func (r *DomainReplayNames) Occupied(key ids.NameKey) bool {
	return r.occupied[key] != 0
}
