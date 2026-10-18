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
	d.VirtualView = true
	for i, name := range []string{"object_catalog", "object_schema", "object_name", "object_type", "dtd_identifier"} {
		id := sqltype.Name
		if i == 3 {
			id = sqltype.VarChar
		}
		typ, ok := (e.catalog.read(e.catalog.state)).LookupType(id)
		if !ok {
			return fmt.Errorf("stuntgres: missing data type privilege carrier")
		}
		usage := typ.Usage()
		if i == 3 {
			usage.Collation.ID = 950
		}
		d.Columns[i] = ColumnDescriptor{Name: name, Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: uint32(d.Address().ObjectID), OriginAttribute: int16(i + 1)}
		d.Index[name] = i
		d.Attributes[i] = int32(i + 1)
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
	if catalog.Owner() == nil || catalog.State() == nil || auth.engine.catalog.owner() != catalog.Owner() || auth.catalog != catalog || auth.roleRecord == nil || source == nil || source.Address() != ref.Address || source.Address().ObjectID != dataTypePrivilegesOID || catalog.Get(ref.Address) != source {
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
			row[i] = sqltype.String(source.Columns[i].Usage.Type, value)
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
	for _, descriptor := range catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *relationDescriptor:
			kind := pgClassKind(catalog, d)
			if kind != "r" && kind != "v" && kind != "f" && kind != "p" && kind != "c" {
				continue
			}
			ns, ok := catalog.Namespace(d.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing data type privilege namespace")
			}
			objectType := "TABLE"
			if kind == "c" {
				objectType = "USER-DEFINED TYPE"
				typ, ok := catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
				if !ok || !typ.SQLType.IsComposite() || typ.OwningRelation != d.Address() {
					return nil, fmt.Errorf("stuntgres: invalid composite privilege identity")
				}
				allowed, err := visible(typ.Address(), typ.Owner, authorization.UsagePrivilege)
				if err != nil {
					return nil, err
				}
				if !allowed {
					continue
				}
			} else if ns.Temporary != (ids.SessionToken{}) && (auth.session == nil || ns.Temporary != auth.session.token) {
				continue
			}
			ownerVisible := kind == "c"
			if !ownerVisible {
				var err error
				ownerVisible, err = auth.roles.InheritsRole(auth.state.CurrentUser, d.Owner)
				if err != nil {
					return nil, err
				}
			}
			for slot := range d.Columns {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				attribute := d.Attributes[slot]
				if attribute <= 0 {
					return nil, fmt.Errorf("stuntgres: invalid data type privilege attribute")
				}
				if !ownerVisible {
					address := d.Address()
					address.SubID = attribute
					rights, err := auth.Effective(ref.Domain().Ref(ref.Epoch, address), false)
					if err != nil {
						return nil, err
					}
					if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) == 0 {
						continue
					}
				}
				emit(ns.Name(), d.Name(), objectType, strconv.FormatInt(int64(attribute), 10))
			}
		case *typeDescriptor:
			if !d.SQLType.IsDomain() {
				continue
			}
			ns, ok := catalog.Namespace(d.Namespace())
			base := d.SQLType.DomainBase().Type
			if !ok || base == nil {
				return nil, fmt.Errorf("stuntgres: invalid domain privilege identity")
			}
			actual, exists := catalog.LookupType(base.ID())
			identity, present := catalog.LookupType(d.SQLType.ID())
			if !exists || actual != base || !present || identity != d.SQLType {
				return nil, fmt.Errorf("stuntgres: inconsistent domain privilege identity")
			}
			// Genuine seeded domains retain PUBLIC USAGE by stable namespace
			// identity even after a schema rename. Display names are not authority;
			// this exception never extends to ownerless user objects.
			seeded := d.SQLType.IsBuiltin() && d.Namespace() == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229})
			if !seeded {
				allowed, err := visible(d.Address(), d.Owner, authorization.UsagePrivilege)
				if err != nil {
					return nil, err
				}
				if !allowed {
					continue
				}
			}
			emit(ns.Name(), d.Name(), "DOMAIN", "1")
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
