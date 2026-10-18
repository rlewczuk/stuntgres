package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native relation identity, independent of PostgreSQL's allocator.
const dataTypePrivilegesOID ids.OID = 16201

func (e *Engine) initializeDataTypePrivileges() error {
	d := e.catalogSourceDescriptor(dataTypePrivilegesOID, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}, "data_type_privileges", 5)
	d.virtualView = true
	for i, name := range []string{"object_catalog", "object_schema", "object_name", "object_type", "dtd_identifier"} {
		id := sqltype.Name
		if i == 3 {
			id = sqltype.VarChar
		}
		typ, ok := (CatalogRead{owner: e, state: e.catalog.state}).LookupType(id)
		if !ok {
			return fmt.Errorf("stuntgres: missing data type privilege carrier")
		}
		usage := typ.Usage()
		if i == 3 {
			usage.Collation.ID = 950
		}
		d.columns[i] = ColumnDescriptor{Name: name, Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: uint32(d.address.ObjectID), OriginAttribute: int16(i + 1)}
		d.index[name] = i
		d.attributes[i] = int32(i + 1)
	}
	e.registerCatalogSource(d)
	return nil
}

// Capture uses only the statement's coherent catalog, role and physical-session
// authority. The caller holds Engine.mu; no live authority is resampled here.
func captureDataTypePrivileges(ctx context.Context, catalog CatalogRead, auth AuthorizationView, database string, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.owner == nil || catalog.state == nil || auth.engine != catalog.owner || auth.catalog != catalog || auth.roleRecord == nil || source == nil || source.address != ref.Address || source.address.ObjectID != dataTypePrivilegesOID || catalog.state.descriptors[ref.Address] != source {
		return nil, fmt.Errorf("stuntgres: invalid data_type_privileges capture")
	}
	// Validate paired roles/catalog/ref even if the inventory contains only
	// seeded domains, whose absent owner is deliberately not an ACL authority.
	if _, err := auth.object(ref); err != nil {
		return nil, err
	}
	if err := auth.roles.ValidatePrincipal(auth.state.CurrentUser); err != nil {
		return nil, err
	}
	var rows [][]sqltype.Value
	emit := func(namespace, name, kind, identifier string) {
		values := [5]string{database, namespace, name, kind, identifier}
		row := make([]sqltype.Value, len(values))
		for i, value := range values {
			row[i] = sqltype.String(source.columns[i].Usage.Type, value)
		}
		rows = append(rows, row)
	}
	visible := func(address ids.ObjectAddress, owner authorization.PrincipalRef, mask authorization.Privileges) (bool, error) {
		owns, err := auth.roles.InheritsRole(auth.state.CurrentUser, owner)
		if err != nil || owns {
			return owns, err
		}
		rights, err := auth.Effective(ref.Domain().Ref(ref.Epoch, address), false)
		return rights&mask != 0, err
	}
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *relationDescriptor:
			kind := pgClassKind(catalog, d)
			if kind != "r" && kind != "v" && kind != "f" && kind != "p" && kind != "c" {
				continue
			}
			ns, ok := catalog.state.descriptors[d.namespace].(*namespaceDescriptor)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing data type privilege namespace")
			}
			objectType := "TABLE"
			if kind == "c" {
				objectType = "USER-DEFINED TYPE"
				typ, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.rowType)}].(*typeDescriptor)
				if !ok || !typ.typ.IsComposite() || typ.owningRelation != d.address {
					return nil, fmt.Errorf("stuntgres: invalid composite privilege identity")
				}
				allowed, err := visible(typ.Address(), typ.owner, authorization.UsagePrivilege)
				if err != nil {
					return nil, err
				}
				if !allowed {
					continue
				}
			} else if ns.temporary != (ids.SessionToken{}) && (auth.session == nil || ns.temporary != auth.session.token) {
				continue
			}
			ownerVisible := kind == "c"
			if !ownerVisible {
				var err error
				ownerVisible, err = auth.roles.InheritsRole(auth.state.CurrentUser, d.owner)
				if err != nil {
					return nil, err
				}
			}
			for slot := range d.columns {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				attribute := d.attributes[slot]
				if attribute <= 0 {
					return nil, fmt.Errorf("stuntgres: invalid data type privilege attribute")
				}
				if !ownerVisible {
					address := d.address
					address.SubID = attribute
					rights, err := auth.Effective(ref.Domain().Ref(ref.Epoch, address), false)
					if err != nil {
						return nil, err
					}
					if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) == 0 {
						continue
					}
				}
				emit(ns.name, d.name, objectType, strconv.FormatInt(int64(attribute), 10))
			}
		case *typeDescriptor:
			if !d.typ.IsDomain() {
				continue
			}
			ns, ok := catalog.state.descriptors[d.Namespace()].(*namespaceDescriptor)
			base := d.typ.DomainBase().Type
			if !ok || base == nil {
				return nil, fmt.Errorf("stuntgres: invalid domain privilege identity")
			}
			actual, exists := catalog.LookupType(base.ID())
			identity, present := catalog.LookupType(d.typ.ID())
			if !exists || actual != base || !present || identity != d.typ {
				return nil, fmt.Errorf("stuntgres: inconsistent domain privilege identity")
			}
			// Genuine seeded domains retain PUBLIC USAGE by stable namespace
			// identity even after a schema rename. Display names are not authority;
			// this exception never extends to ownerless user objects.
			seeded := d.typ.IsBuiltin() && d.Namespace() == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229})
			if !seeded {
				allowed, err := visible(d.Address(), d.owner, authorization.UsagePrivilege)
				if err != nil {
					return nil, err
				}
				if !allowed {
					continue
				}
			}
			emit(ns.name, d.Name(), "DOMAIN", "1")
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
