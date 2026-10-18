package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type domainValidationUse struct {
	relation   *relationDescriptor
	columns    []int
	storage    *relationStorage
	versions   []rowVersion
	ends       *deletionRoot
	generation uint64
}

// The observation is the incoming type-use graph, not a catalog-wide epoch
// comparison. Unrelated declarations can publish without replaying CHECK truth.
type domainValidationFence struct {
	target          ids.ObjectAddress
	objects         map[ids.ObjectAddress]Descriptor
	uses            []domainValidationUse
	path            []ids.ObjectAddress
	reads           []*relationDescriptor
	allowContainers bool // Committed predecessors may be hidden by prior own DDL.
}

func domainUsesAt(view CatalogRead, target ids.ObjectAddress, path []ids.ObjectAddress) (*domainValidationFence, error) {
	fence := &domainValidationFence{target: target, objects: make(map[ids.ObjectAddress]Descriptor), path: path}
	cache := make(map[sqltype.ID]sqltype.ID)
	active := make(map[sqltype.ID]bool)
	var contains func(sqltype.ID) (sqltype.ID, error)
	contains = func(id sqltype.ID) (sqltype.ID, error) {
		if result, ok := cache[id]; ok {
			return result, nil
		}
		if active[id] {
			return 0, fmt.Errorf("stuntgres: cyclic domain use graph")
		}
		d, ok := view.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(id)}].(*typeDescriptor)
		if !ok {
			return 0, fmt.Errorf("stuntgres: missing domain use type")
		}
		active[id] = true
		var found sqltype.ID
		var err error
		switch {
		case d.Address() == target:
			found = id
		case d.typ.IsDomain():
			found, err = contains(d.typ.DomainBase().Type.ID())
			if found != 0 && domainDescendsFrom(d.typ, target.ObjectID) {
				found = id
			}
		case d.typ.IsArray():
			found, err = contains(d.typ.Element().ID())
		case d.owningRelation != (ids.ObjectAddress{}):
			relation, ok := view.state.descriptors[d.owningRelation].(*relationDescriptor)
			if !ok {
				return 0, fmt.Errorf("stuntgres: missing domain use rowtype owner")
			}
			for _, column := range relation.columns {
				found, err = contains(column.Usage.Type.ID())
				if err != nil || found != 0 {
					break
				}
			}
		}
		delete(active, id)
		if err != nil {
			return 0, err
		}
		cache[id] = found
		if found != 0 {
			fence.objects[d.Address()] = d
		}
		return found, nil
	}
	// Include unstored derived/container declarations as graph edges too. Their
	// later storage cannot evade the final observation by retaining an old OID.
	for address, d := range view.state.descriptors {
		if _, ok := d.(*typeDescriptor); ok {
			if _, err := contains(sqltype.ID(address.ObjectID)); err != nil {
				return nil, err
			}
		}
	}
	var relations []*relationDescriptor
	for _, d := range view.state.descriptors {
		if relation, ok := d.(*relationDescriptor); ok {
			relations = append(relations, relation)
		}
	}
	slices.SortFunc(relations, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	var blocker error
	for _, relation := range relations {
		physical := relation.relationKind == tableRelation || relation.relationKind == materializedViewRelation
		stored := physical || relation.relationKind == partitionedRelation
		var columns []int
		recorded := false
		for i, column := range relation.columns {
			found := cache[column.Usage.Type.ID()]
			if found == 0 {
				continue
			}
			fence.objects[relation.address] = relation
			if !recorded {
				fence.reads = append(fence.reads, relation)
				recorded = true
			}
			if domainDescendsFrom(column.Usage.Type, target.ObjectID) {
				if physical {
					columns = append(columns, i)
				}
			} else if stored {
				d := view.state.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(found)}].(*typeDescriptor)
				name := typeDiagnosticNameAt(view, d, path)
				if blocker == nil {
					blocker = sqlError("0A000", fmt.Sprintf("cannot alter type %q because column %q uses it", name, relation.name+"."+column.Name))
				}
			}
		}
		if len(columns) != 0 {
			fence.uses = append(fence.uses, domainValidationUse{relation: relation, columns: columns})
		}
	}
	return fence, blocker
}

func domainDescendsFrom(typ *sqltype.Descriptor, target ids.OID) bool {
	for typ.IsDomain() {
		if ids.OID(typ.ID()) == target {
			return true
		}
		typ = typ.DomainBase().Type
	}
	return false
}
func domainGraphEqual(a, b *domainValidationFence) bool {
	return a.target == b.target && maps.Equal(a.objects, b.objects)
}

func (f *domainValidationFence) current(view CatalogRead, storage map[ids.ObjectAddress]*relationStorage) bool {
	next, err := domainUsesAt(view, f.target, f.path)
	if next == nil || err != nil && !f.allowContainers || !maps.Equal(f.objects, next.objects) {
		return false
	}
	for _, use := range f.uses {
		store := storage[use.relation.address]
		if store != use.storage || store == nil || len(store.versions) != len(use.versions) || store.ends != use.ends || store.generation != use.generation {
			return false
		}
	}
	return true
}

func domainHistoricalViolation(view CatalogRead, relation *relationDescriptor, column int) error {
	diagnostic := sqlError("23514", fmt.Sprintf("column %q of table %q contains values that violate the new constraint", relation.columns[column].Name, relation.name)).(*sqlstate.Error)
	diagnostic.Schema = view.state.descriptors[relation.namespace].Name()
	diagnostic.Table, diagnostic.Column = relation.name, relation.columns[column].Name
	return diagnostic
}
