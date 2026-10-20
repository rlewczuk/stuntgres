package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) initializeCatalogSources() error {
	for _, spec := range catalogSourceSpecs {
		d := e.catalogSourceDescriptor(spec.oid, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: spec.namespace}, spec.name, len(spec.columns))
		d.History.HasIndexes, d.VirtualView = spec.indexes, spec.virtual
		if spec.bootstrapOwner {
			d.Owner = e.roles.domain.Principal(authorization.BootstrapPrincipalID)
			d.Security.ACL.Entries[0].Grantor = d.Owner
		}
		if spec.noPublic {
			d.Security.ACL = authorization.ACLValue{}
		}
		for i, column := range spec.columns {
			typ, ok := e.catalog.read(e.catalog.state).LookupType(column.typ)
			if !ok {
				if spec.namespace == ids.PGCatalogNamespaceOID && (spec.oid == ids.ForeignWrapperClassID || spec.oid == ids.ForeignServerClassID || spec.oid == ids.ForeignMappingClassID || spec.oid == ids.ForeignTableClassID || spec.oid == pgUserMappingsOID) {
					return fmt.Errorf(spec.missingType, column.typ)
				}
				return fmt.Errorf("%s", spec.missingType)
			}
			usage := typ.Usage()
			if column.collation != 0 {
				usage.Collation.ID = column.collation
			}
			if column.typmod != -1 {
				usage.Typmod = column.typmod
			}
			d.Columns[i] = catalog.Column{Name: column.name, Usage: usage, Nullable: column.nullable, NullabilityKnown: true, OriginTable: uint32(spec.oid), OriginAttribute: int16(i + 1), DeclaredDimensions: column.dimensions}
			d.Index[column.name], d.Attributes[i] = i, int32(i+1)
		}
		e.registerCatalogSource(d)
	}
	return nil
}

func (e *Engine) catalogSourceDescriptor(oid ids.OID, namespace ids.ObjectAddress, name string, count int) *catalog.Relation {
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectName: name, ObjectRevision: 1, RelationKind: catalog.CatalogRelation, Owner: owner, Index: make(map[string]int, count), Attributes: make([]int32, count), Columns: make([]catalog.Column, count), ColumnMeta: catalog.ColumnMetadata{HighWater: int32(count)}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}}}
}

func (e *Engine) registerCatalogSource(d *catalog.Relation) {
	initializeCatalogAttributes(d)
	if address, ok := e.catalog.state.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: d.Namespace(), Name: d.Name()}); ok {
		row := e.catalog.state.MustType(address)
		row.OwningRelation, row.Owner = d.Address(), d.Owner
		d.RowType = row.SQLType.ID()
		if row.Array != 0 {
			array := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
			array.OwningRelation, array.Owner = d.Address(), d.Owner
		}
	}
	e.catalog.state.SetDescriptor(d.Address(), d)
	e.catalog.state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
}

func (e *Engine) captureCatalogSourceLocked(ctx context.Context, view catalog.Read, roles authorization.Read, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
	produce, ok := catalogSourceProducerFor(source.Address().ObjectID)
	if !ok {
		return nil, fmt.Errorf("stuntgres: unknown catalog source")
	}
	in := catalogSourceInput{Context: ctx, Catalog: view, Roles: roles, Owner: e.catalog.owner(), Ref: ref, Source: source, Runtime: engineCatalogRuntime{engine: e, sequenceBase: view.State()}}
	return captureCatalogSourceRows(in, produce)
}

func nonuniqueConstraintProvenance(catalog catalog.Read, table *catalog.Relation, name string, attribute int32, check bool) (bool, int64) {
	if graph := table.Inheritance; graph != nil {
		provenance, exists := graph.NotNull[attribute]
		if check {
			provenance, exists = graph.Checks[name]
		}
		if exists {
			return provenance.Local, int64(provenance.Count())
		}
	}
	if table.Membership != nil {
		parent := catalog.MustRelation(table.Membership.Parent)
		if check {
			for _, constraint := range parent.Constraints.Check {
				if constraint.Name == name {
					return false, 1
				}
			}
		} else {
			slot := slices.Index(table.Attributes, attribute)
			if slot >= 0 {
				parentSlot, exists := parent.Index[table.Columns[slot].Name]
				if exists {
					parentAttribute := parent.Attributes[parentSlot]
					for _, constraint := range parent.Constraints.NotNull {
						if constraint.Attribute == parentAttribute {
							return false, 1
						}
					}
				}
			}
		}
	}
	return true, 0
}
