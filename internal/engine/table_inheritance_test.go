package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func inheritanceUnitRelation(e *core.Engine, name string, id ids.OID, names ...string) *catalog.Relation {
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Index: make(map[string]int)}
	for i, name := range names {
		d.Index[name] = i
		d.Attributes = append(d.Attributes, int32(i+1))
		d.Columns = append(d.Columns, catalog.Column{Name: name, Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: uint32(id), OriginAttribute: int16(i + 1)})
	}
	d.ColumnMeta.HighWater = int32(len(names))
	return d
}

func inheritanceUnitView(e *core.Engine, relations ...*catalog.Relation) catalog.Read {
	state := catalog.CloneState(e.CatalogRead().State())
	for _, d := range relations {
		state.SetDescriptor(d.Address(), d)
		state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
	}
	return e.Catalog.Read(state)
}
