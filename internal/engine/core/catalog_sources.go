package core

import (
	"fmt"
	"slices"
	"sync"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func (e *Engine) initializeCatalogSources() error {
	roles := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	postgres := roles.LookupRef("postgres")
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for i, template := range catalogSourceTemplates() {
		spec := &pgcatalog.Specs[i]
		d := new(catalog.Relation)
		*d = template
		d.Owner = postgres
		if spec.BootstrapOwner {
			d.Owner = e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
		}
		d.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
		if spec.NoPublic {
			d.Security.ACL = authorization.ACLValue{}
		}
		if spec.OID == pgcatalog.SubscriptionOID {
			d.Security.ACL = authorization.MaterializeRelationACL(d.Security.ACL, d.Owner)
			if err := authorization.ValidateACL(roles, d.Security.ACL, authorization.TableGrantPrivileges); err != nil {
				return err
			}
			d.Security.ColumnACL = make(map[int32]authorization.ACLValue, 17)
			for attribute := int32(1); attribute <= 18; attribute++ {
				if attribute == 14 {
					continue
				}
				acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
				if err := authorization.ValidateACL(roles, acl, authorization.SelectPrivilege); err != nil {
					return err
				}
				d.Security.ColumnACL[attribute] = acl
			}
		}
		if spec.OID == pgcatalog.SubscriptionRelOID || spec.OID == pgcatalog.PublicationOID || spec.OID == pgcatalog.PublicationNamespaceOID || spec.OID == pgcatalog.PublicationRelationOID || spec.OID == pgcatalog.PublicationTablesOID {
			d.Security.ACL = authorization.MaterializeRelationACL(authorization.ACLValue{}, d.Owner)
			d.Security.ACL.Entries = append(d.Security.ACL.Entries, authorization.ACLEntry{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege})
			if err := authorization.ValidateACL(roles, d.Security.ACL, authorization.TableGrantPrivileges); err != nil {
				return err
			}
		}
		if spec.OID == pgcatalog.ShmemAllocationsOID || spec.OID == pgcatalog.ShmemAllocationsNUMAOID {
			stats := roles.LookupRef("pg_read_all_stats")
			d.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: d.Owner}, Privileges: authorization.TableGrantPrivileges},
				{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.SelectPrivilege},
			}}
			if err := authorization.ValidateACL(roles, d.Security.ACL, authorization.TableGrantPrivileges); err != nil {
				return err
			}
		}
		d.Columns = slices.Clone(template.Columns)
		for j, column := range spec.Columns {
			typ, ok := types[column.Type]
			if !ok {
				if typ, ok = e.Catalog.Current().LookupType(column.Type); !ok {
					if spec.Namespace == ids.PGCatalogNamespaceOID && (spec.OID == ids.ForeignWrapperClassID || spec.OID == ids.ForeignServerClassID || spec.OID == ids.ForeignMappingClassID || spec.OID == ids.ForeignTableClassID || spec.OID == pgcatalog.UserMappingsOID) {
						return fmt.Errorf(spec.MissingType, column.Type)
					}
					return fmt.Errorf("%s", spec.MissingType)
				}
				types[column.Type] = typ
			}
			d.Columns[j].Usage.Type = typ
		}
		catalog.InstallCatalogSource(e.Catalog.State, d)
	}
	return nil
}

// catalogSourceTemplates holds, for each pgcatalog.Specs entry, its catalog
// relation without an owner or ACL and with column types of a template
// generation; each engine copies it with its own principals and column types.
// The spec fixes everything else, so engines share the index map and the
// attribute slices, which writers copy before changing.
var catalogSourceTemplates = sync.OnceValue(func() []catalog.Relation {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	templates := make([]catalog.Relation, len(pgcatalog.Specs))
	for i := range pgcatalog.Specs {
		spec := &pgcatalog.Specs[i]
		count := len(spec.Columns)
		d := &templates[i]
		*d = catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: spec.OID}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: spec.Namespace}, ObjectName: spec.Name, ObjectRevision: 1, RelationKind: catalog.CatalogRelation, Index: make(map[string]int, count), Attributes: make([]int32, count), Columns: make([]catalog.Column, count), ColumnMeta: catalog.ColumnMetadata{HighWater: int32(count)}}
		d.History.HasIndexes, d.VirtualView = spec.Indexes, spec.Virtual
		complete := true
		for j, column := range spec.Columns {
			typ := types[column.Type]
			if typ == nil {
				// The engine reports the spec's missing-type error.
				complete = false
				continue
			}
			usage := typ.Usage()
			if column.Collation != 0 {
				usage.Collation.ID = column.Collation
			}
			if column.Typmod != -1 {
				usage.Typmod = column.Typmod
			}
			d.Columns[j] = catalog.Column{Name: column.Name, Usage: usage, Nullable: column.Nullable, NullabilityKnown: true, OriginTable: uint32(spec.OID), OriginAttribute: int16(j + 1), DeclaredDimensions: column.Dimensions}
			d.Index[column.Name], d.Attributes[j] = j, int32(j+1)
		}
		if complete {
			pgcatalog.InitializeAttributes(d)
		}
	}
	return templates
})
