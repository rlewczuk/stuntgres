package pgcatalog

import (
	"fmt"
	"slices"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native relation identity, independent of PostgreSQL's allocator.
const DataTypePrivilegesOID ids.OID = 16201

var dataTypePrivilegesColumns = []ColumnSpec{
	{"object_catalog", 13248, true, 950, 0, -1},
	{"object_schema", 13248, true, 950, 0, -1},
	{"object_name", 13248, true, 950, 0, -1},
	{"object_type", 13246, true, 950, 0, -1},
	{"dtd_identifier", 13248, true, 950, 0, -1},
}

// Capture uses only the statement's coherent catalog, role and physical-session
// authority. The caller holds Engine.mu; no live authority is resampled here.
func captureDataTypePrivileges(in Input) ([][]sqltype.Value, error) {
	ctx, view, ref, source := in.Context, in.Catalog, in.Ref, in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing data type privileges runtime")
	}
	if err := in.Runtime.ValidateCapture(view, in.Roles, ref, source, DataTypePrivilegesOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != view.Owner() || source == nil || !source.VirtualView || len(source.Columns) != 5 || len(source.Attributes) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid data type privileges source")
	}
	for i, expected := range dataTypePrivilegesColumns {
		column := source.Columns[i]
		typ, present := view.LookupType(expected.Type)
		baseID := sqltype.Name
		if i == 3 {
			baseID = sqltype.VarChar
		}
		base, exists := view.LookupType(baseID)
		if !present || !exists || !typ.IsDomain() || typ.DomainBase().Type != base || column.Name != expected.Name || column.Usage != typ.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(DataTypePrivilegesOID) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid data type privileges source domain")
		}
	}
	nulls, err := domainNulls(source.Columns)
	if err != nil {
		return nil, err
	}
	database := in.Runtime.DatabaseName()
	var rows [][]sqltype.Value
	emit := func(namespace, name, kind, identifier string, hasIdentifier bool) error {
		row := slices.Clone(nulls)
		values := [5]string{database, namespace, name, kind, identifier}
		for i, value := range values {
			if i != 4 || hasIdentifier {
				row[i] = sqltype.String(source.Columns[i].Usage.Type.BaseType(), value)
			}
		}
		if err := wrapDomainCells(source.Columns, row); err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	}
	visible := func(address ids.ObjectAddress, owner authorization.PrincipalRef, mask authorization.Privileges) (bool, error) {
		owns, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), owner)
		if err != nil || owns {
			return owns, err
		}
		rights, err := in.Runtime.Effective(ref.Domain().Ref(ref.Epoch, address), false)
		return rights&mask != 0, err
	}
	for address, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.Relation:
			kind := ClassKind(view, d)
			if kind != "r" && kind != "v" && kind != "f" && kind != "p" && kind != "c" {
				continue
			}
			if address != d.Address() || len(d.Attributes) != len(d.Columns) {
				return nil, fmt.Errorf("stuntgres: invalid data type privilege relation")
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
				if _, dropped := d.ColumnMeta.Dropped[d.Attributes[slot]]; dropped {
					continue
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
				if err := emit(ns.Name(), d.Name(), objectType, strconv.FormatInt(int64(attribute), 10), true); err != nil {
					return nil, err
				}
			}
		case *catalog.Type:
			if !d.SQLType.IsDomain() {
				continue
			}
			if address != d.Address() || address.ObjectID != ids.OID(d.SQLType.ID()) {
				return nil, fmt.Errorf("stuntgres: invalid domain privilege address")
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
			allowed, err := visible(d.Address(), d.Owner, authorization.UsagePrivilege)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			if err := emit(ns.Name(), d.Name(), "DOMAIN", "1", true); err != nil {
				return nil, err
			}
		case *catalog.Procedure:
			if address != d.Address() {
				return nil, fmt.Errorf("stuntgres: invalid data type privilege routine")
			}
			allowed, err := visible(address, d.Owner, authorization.ExecutePrivilege)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			switch d.RoutineKind {
			case catalog.FunctionRoutine, catalog.ProcedureRoutine, catalog.AggregateRoutine, catalog.WindowRoutine:
			default:
				return nil, fmt.Errorf("stuntgres: invalid data type privilege routine kind")
			}
			namespace, ok := view.Namespace(d.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing data type privilege routine namespace")
			}
			identity := routineSpecificName(d.Name(), address.ObjectID)
			if err := VisitProcedureFormals(ctx, d, sqltype.Value{}, func(f ProcedureFormal) error {
				declared, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: f.Type})
				if !ok {
					return fmt.Errorf("stuntgres: missing data type privilege formal type")
				}
				if err := in.Runtime.ValidateType(view, declared); err != nil {
					return err
				}
				if f.Usage.Type != nil && f.Usage.Type != declared.SQLType {
					return fmt.Errorf("stuntgres: foreign data type privilege formal usage")
				}
				return emit(namespace.Name(), identity, "ROUTINE", strconv.Itoa(f.Index+1), true)
			}); err != nil {
				return nil, err
			}
			if d.RoutineKind != catalog.ProcedureRoutine {
				result, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result})
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing data type privilege result type")
				}
				if err := in.Runtime.ValidateType(view, result); err != nil {
					return nil, err
				}
			}
			if err := emit(namespace.Name(), identity, "ROUTINE", "0", d.RoutineKind != catalog.ProcedureRoutine); err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}
