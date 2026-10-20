package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Replay names are transient rendering predictions keyed by captured identity,
// not a catalog or destination OID allocator. Only domain/enum creators run here.
type domainReplayNames struct {
	occupied map[ids.NameKey]sqltype.ID
	names    map[sqltype.ID]ids.NameKey
	arrays   map[sqltype.ID]sqltype.ID
	elements map[sqltype.ID]sqltype.ID
}

func newDomainReplayNames() *domainReplayNames {
	r := &domainReplayNames{
		occupied: make(map[ids.NameKey]sqltype.ID), names: make(map[sqltype.ID]ids.NameKey),
		arrays: make(map[sqltype.ID]sqltype.ID), elements: make(map[sqltype.ID]sqltype.ID),
	}
	for _, typ := range sqltype.Builtins() {
		namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		if typ.NamespaceName() == "information_schema" {
			namespace.ObjectID = 13229
		}
		key := ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: typ.Name()}
		r.occupied[key], r.names[typ.ID()] = typ.ID(), key
		r.arrays[typ.ID()] = typ.AssociatedArray()
		if typ.IsArray() {
			r.elements[typ.ID()] = typ.Element().ID()
		}
	}
	return r
}

func (r *domainReplayNames) arrayName(key ids.NameKey) ids.NameKey {
	key.Name = chooseAssociatedArrayName(key.Name, func(candidate string) bool {
		key.Name = candidate
		return r.occupied[key] != 0
	})
	return key
}

func (r *domainReplayNames) reserve(d *catalog.Type) error {
	key := catalog.DescriptorNameKey(d)
	if incumbent := r.occupied[key]; incumbent != 0 {
		element := r.elements[incumbent]
		if element == 0 || r.arrays[element] != incumbent {
			return fmt.Errorf("stuntgres: conflicting domain replay type name")
		}
		moved := r.arrayName(key)
		delete(r.occupied, key)
		r.occupied[moved], r.names[incumbent] = incumbent, moved
	}
	r.occupied[key], r.names[d.SQLType.ID()] = d.SQLType.ID(), key
	return nil
}

func (r *domainReplayNames) associate(d *catalog.Type) {
	if d.Array == 0 {
		return
	}
	key := r.arrayName(r.names[d.SQLType.ID()])
	r.occupied[key], r.names[d.Array] = d.Array, key
	r.arrays[d.SQLType.ID()], r.elements[d.Array] = d.Array, d.SQLType.ID()
}
