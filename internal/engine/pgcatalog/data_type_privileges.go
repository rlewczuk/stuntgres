package pgcatalog

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native relation identity, independent of PostgreSQL's allocator.
const DataTypePrivilegesOID ids.OID = 16201

// Capture uses only the statement's coherent catalog, role and physical-session
// authority. The caller holds Engine.mu; no live authority is resampled here.
func captureDataTypePrivileges(in Input) ([][]sqltype.Value, error) {
	ctx, view, ref, source := in.Context, in.Catalog, in.Ref, in.Source
	database := in.Runtime.DatabaseName()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(view, in.Roles, ref, source, DataTypePrivilegesOID); err != nil {
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
		owns, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), owner)
		if err != nil || owns {
			return owns, err
		}
		rights, err := in.Runtime.Effective(ref.Domain().Ref(ref.Epoch, address), false)
		return rights&mask != 0, err
	}
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.Relation:
			kind := ClassKind(view, d)
			if kind != "r" && kind != "v" && kind != "f" && kind != "p" && kind != "c" {
				continue
			}
			ns, ok := view.Namespace(d.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing data type privilege namespace")
			}
			objectType := "TABLE"
			if kind == "c" {
				objectType = "USER-DEFINED TYPE"
				typ, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
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
			} else if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != in.Runtime.TemporaryOwner() {
				continue
			}
			ownerVisible := kind == "c"
			if !ownerVisible {
				var err error
				ownerVisible, err = in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), d.Owner)
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
					rights, err := in.Runtime.Effective(ref.Domain().Ref(ref.Epoch, address), false)
					if err != nil {
						return nil, err
					}
					if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) == 0 {
						continue
					}
				}
				emit(ns.Name(), d.Name(), objectType, strconv.FormatInt(int64(attribute), 10))
			}
		case *catalog.Type:
			if !d.SQLType.IsDomain() {
				continue
			}
			ns, ok := view.Namespace(d.Namespace())
			base := d.SQLType.DomainBase().Type
			if !ok || base == nil {
				return nil, fmt.Errorf("stuntgres: invalid domain privilege identity")
			}
			actual, exists := view.LookupType(base.ID())
			identity, present := view.LookupType(d.SQLType.ID())
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
	return rows, nil
}
