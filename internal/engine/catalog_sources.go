package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func (e *Engine) initializeCatalogSources() error {
	for _, spec := range pgcatalog.Specs {
		d := e.catalogSourceDescriptor(spec.OID, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: spec.Namespace}, spec.Name, len(spec.Columns))
		d.History.HasIndexes, d.VirtualView = spec.Indexes, spec.Virtual
		if spec.BootstrapOwner {
			d.Owner = e.roles.domain.Principal(authorization.BootstrapPrincipalID)
			d.Security.ACL.Entries[0].Grantor = d.Owner
		}
		if spec.NoPublic {
			d.Security.ACL = authorization.ACLValue{}
		}
		for i, column := range spec.Columns {
			typ, ok := e.catalog.read(e.catalog.state).LookupType(column.Type)
			if !ok {
				if spec.Namespace == ids.PGCatalogNamespaceOID && (spec.OID == ids.ForeignWrapperClassID || spec.OID == ids.ForeignServerClassID || spec.OID == ids.ForeignMappingClassID || spec.OID == ids.ForeignTableClassID || spec.OID == pgcatalog.UserMappingsOID) {
					return fmt.Errorf(spec.MissingType, column.Type)
				}
				return fmt.Errorf("%s", spec.MissingType)
			}
			usage := typ.Usage()
			if column.Collation != 0 {
				usage.Collation.ID = column.Collation
			}
			if column.Typmod != -1 {
				usage.Typmod = column.Typmod
			}
			d.Columns[i] = catalog.Column{Name: column.Name, Usage: usage, Nullable: column.Nullable, NullabilityKnown: true, OriginTable: uint32(spec.OID), OriginAttribute: int16(i + 1), DeclaredDimensions: column.Dimensions}
			d.Index[column.Name], d.Attributes[i] = i, int32(i+1)
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
	pgcatalog.InitializeAttributes(d)
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

func (e *Engine) captureCatalogSourceLocked(ctx context.Context, view catalog.Read, roles authorization.Read, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	produce, ok := pgcatalog.ProducerFor(source.Address().ObjectID)
	if !ok {
		return nil, fmt.Errorf("stuntgres: unknown catalog source")
	}
	in := pgcatalog.Input{Context: ctx, Catalog: view, Roles: roles, Owner: e.catalog.owner(), Ref: ref, Source: source, Runtime: engineCatalogRuntime{engine: e, sequenceBase: view.State()}}
	return captureCatalogSourceRows(in, produce)
}
