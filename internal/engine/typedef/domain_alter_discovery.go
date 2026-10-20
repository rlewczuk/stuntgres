package typedef

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type DomainValidationUse struct {
	Relation   *catalog.Relation
	Columns    []int
	Storage    *storage.Relation
	Versions   []storage.RowVersion
	Ends       *storage.DeletionRoot
	Generation uint64
}

// The observation is the incoming type-use graph, not a catalog-wide epoch
// comparison. Unrelated declarations can publish without replaying CHECK truth.
type DomainValidationFence struct {
	Target          ids.ObjectAddress
	Objects         map[ids.ObjectAddress]catalog.Descriptor
	Uses            []DomainValidationUse
	Path            []ids.ObjectAddress
	Reads           []*catalog.Relation
	AllowContainers bool // Committed predecessors may be hidden by prior own DDL.
}

func DomainUsesAt(view catalog.Read, target ids.ObjectAddress, path []ids.ObjectAddress) (*DomainValidationFence, error) {
	fence := &DomainValidationFence{Target: target, Objects: make(map[ids.ObjectAddress]catalog.Descriptor), Path: path}
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
		d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		if !ok {
			return 0, fmt.Errorf("stuntgres: missing domain use type")
		}
		active[id] = true
		var found sqltype.ID
		var err error
		switch {
		case d.Address() == target:
			found = id
		case d.SQLType.IsDomain():
			found, err = contains(d.SQLType.DomainBase().Type.ID())
			if found != 0 && domainDescendsFrom(d.SQLType, target.ObjectID) {
				found = id
			}
		case d.SQLType.IsArray():
			found, err = contains(d.SQLType.Element().ID())
		case d.OwningRelation != (ids.ObjectAddress{}):
			relation, ok := view.Relation(d.OwningRelation)
			if !ok {
				return 0, fmt.Errorf("stuntgres: missing domain use rowtype owner")
			}
			for _, column := range relation.Columns {
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
			fence.Objects[d.Address()] = d
		}
		return found, nil
	}
	// Include unstored derived/container declarations as graph edges too. Their
	// later storage cannot evade the final observation by retaining an old OID.
	for address, d := range view.Descriptors() {
		if _, ok := d.(*catalog.Type); ok {
			if _, err := contains(sqltype.ID(address.ObjectID)); err != nil {
				return nil, err
			}
		}
	}
	var relations []*catalog.Relation
	for _, d := range view.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok {
			relations = append(relations, relation)
		}
	}
	slices.SortFunc(relations, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	var blocker error
	for _, relation := range relations {
		physical := relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.MaterializedViewRelation
		stored := physical || relation.RelationKind == catalog.PartitionedRelation
		var columns []int
		recorded := false
		for i, column := range relation.Columns {
			found := cache[column.Usage.Type.ID()]
			if found == 0 {
				continue
			}
			fence.Objects[relation.Address()] = relation
			if !recorded {
				fence.Reads = append(fence.Reads, relation)
				recorded = true
			}
			if domainDescendsFrom(column.Usage.Type, target.ObjectID) {
				if physical {
					columns = append(columns, i)
				}
			} else if stored {
				d := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(found)})
				name := catalog.TypeDiagnosticNameAt(view, d, path)
				if blocker == nil {
					blocker = sqlError("0A000", fmt.Sprintf("cannot alter type %q because column %q uses it", name, relation.Name()+"."+column.Name))
				}
			}
		}
		if len(columns) != 0 {
			fence.Uses = append(fence.Uses, DomainValidationUse{Relation: relation, Columns: columns})
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
func DomainGraphEqual(a, b *DomainValidationFence) bool {
	return a.Target == b.Target && maps.Equal(a.Objects, b.Objects)
}

func (f *DomainValidationFence) Current(view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation) bool {
	next, err := DomainUsesAt(view, f.Target, f.Path)
	if next == nil || err != nil && !f.AllowContainers || !maps.Equal(f.Objects, next.Objects) {
		return false
	}
	for _, use := range f.Uses {
		store := relations[use.Relation.Address()]
		if store != use.Storage || store == nil || len(store.Versions) != len(use.Versions) || store.Ends != use.Ends || store.Generation != use.Generation {
			return false
		}
	}
	return true
}

func DomainHistoricalViolation(view catalog.Read, relation *catalog.Relation, column int) error {
	diagnostic := sqlError("23514", fmt.Sprintf("column %q of table %q contains values that violate the new constraint", relation.Columns[column].Name, relation.Name())).(*sqlstate.Error)
	diagnostic.Schema = view.Get(relation.Namespace()).Name()
	diagnostic.Table, diagnostic.Column = relation.Name(), relation.Columns[column].Name
	return diagnostic
}
