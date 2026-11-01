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

// InfoElementTypesOID is a native source identity, not a PostgreSQL allocation.
const InfoElementTypesOID ids.OID = 16256

var infoElementTypesColumns = []ColumnSpec{
	{"object_catalog", 13248, true, 950, 0, -1},
	{"object_schema", 13248, true, 950, 0, -1},
	{"object_name", 13248, true, 950, 0, -1},
	{"object_type", 13246, true, 950, 0, -1},
	{"collection_type_identifier", 13248, true, 950, 0, -1},
	{"data_type", 13246, true, 950, 0, -1},
	{"character_maximum_length", 13243, true, 0, 0, -1},
	{"character_octet_length", 13243, true, 0, 0, -1},
	{"character_set_catalog", 13248, true, 950, 0, -1},
	{"character_set_schema", 13248, true, 950, 0, -1},
	{"character_set_name", 13248, true, 950, 0, -1},
	{"collation_catalog", 13248, true, 950, 0, -1},
	{"collation_schema", 13248, true, 950, 0, -1},
	{"collation_name", 13248, true, 950, 0, -1},
	{"numeric_precision", 13243, true, 0, 0, -1},
	{"numeric_precision_radix", 13243, true, 0, 0, -1},
	{"numeric_scale", 13243, true, 0, 0, -1},
	{"datetime_precision", 13243, true, 0, 0, -1},
	{"interval_type", 13246, true, 950, 0, -1},
	{"interval_precision", 13243, true, 0, 0, -1},
	{"udt_catalog", 13248, true, 950, 0, -1},
	{"udt_schema", 13248, true, 950, 0, -1},
	{"udt_name", 13248, true, 950, 0, -1},
	{"scope_catalog", 13248, true, 950, 0, -1},
	{"scope_schema", 13248, true, 950, 0, -1},
	{"scope_name", 13248, true, 950, 0, -1},
	{"maximum_cardinality", 13243, true, 0, 0, -1},
	{"dtd_identifier", 13248, true, 950, 0, -1},
}

// Capture walks actual declarations in one paired catalog/role frontier. It
// neither unwraps nominal declaration domains nor borrows live session state.
func captureInfoElementTypes(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing element_types runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoElementTypesOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.TypeNamePath == nil || !in.Source.VirtualView || len(in.Source.Columns) != len(infoElementTypesColumns) || len(in.Source.Attributes) != len(infoElementTypesColumns) || len(in.Source.ColumnMeta.CatalogAttributes) != len(infoElementTypesColumns) {
		return nil, fmt.Errorf("stuntgres: invalid element_types source")
	}
	for _, address := range in.TypeNamePath {
		if _, ok := in.Catalog.Namespace(address); !ok || address.ClassID != ids.NamespaceClassID || address.SubID != 0 {
			return nil, fmt.Errorf("stuntgres: invalid element_types type-name path")
		}
	}
	for i, expected := range infoElementTypesColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(expected.Type)
		baseID := sqltype.VarChar
		switch expected.Type {
		case 13248:
			baseID = sqltype.Name
		case 13243:
			baseID = sqltype.Int4
		}
		base, exists := in.Catalog.LookupType(baseID)
		if !ok || !exists || !domain.IsDomain() || domain.DomainBase().Type != base || domain.DomainBase().Typmod != -1 || domain.OwnDomainNotNull() || column.Name != expected.Name || column.Usage != domain.Usage() || column.Usage.Typmod != -1 || column.Usage.Collation.ID != expected.Collation || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoElementTypesOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid element_types source domain")
		}
		attribute := AttributeMetadata(in.Source, i)
		if attribute.NotNull || attribute.Dimensions != 0 || attribute.Collation != expected.Collation || attribute.Typmod != -1 {
			return nil, fmt.Errorf("stuntgres: invalid element_types source attribute")
		}
	}
	nulls, err := domainNulls(in.Source.Columns)
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	output := in.Runtime.TypeOutput(in.Owner, in.Catalog, in.Roles)
	resolve := func(id ids.OID, usage sqltype.Usage) (*catalog.Type, error) {
		declared, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing element_types declaration type")
		}
		if err := in.Runtime.ValidateType(in.Catalog, declared); err != nil {
			return nil, err
		}
		if usage.Type != nil && usage.Type != declared.SQLType {
			return nil, fmt.Errorf("stuntgres: foreign element_types declaration usage")
		}
		return declared, nil
	}
	emit := func(schema, name, kind string, ordinal int, declared *catalog.Type, collation uint32) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		admitted, err := infoElementTypesMetadataAdmitted(in, declared)
		if err != nil || !admitted {
			return err
		}
		facts, err := TypeFacts(in.Context, in.Catalog, declared, output)
		if err != nil {
			return err
		}
		// This is the released nominal predicate, including catalog vectors.
		if facts.Element == 0 || facts.Length != -1 {
			return nil
		}
		element, err := resolve(ids.OID(facts.Element), sqltype.Usage{})
		if err != nil {
			return err
		}
		ns, ok := in.Catalog.Namespace(element.Namespace())
		if !ok {
			return fmt.Errorf("stuntgres: missing element_types element namespace")
		}
		dataType := "USER-DEFINED"
		if ns.Address().ObjectID == ids.PGCatalogNamespaceOID {
			dataType, err = infoDomainsBaseName(in.Catalog, element, in.TypeNamePath)
			if err != nil {
				return err
			}
		}
		row := slices.Clone(nulls)
		text := func(i int, value string) { row[i] = sqltype.String(in.Source.Columns[i].Usage.Type.BaseType(), value) }
		identifier := strconv.Itoa(ordinal)
		text(0, database)
		text(1, schema)
		text(2, name)
		text(3, kind)
		text(4, identifier)
		text(5, dataType)
		text(20, database)
		text(21, ns.Name())
		text(22, element.Name())
		text(27, "a"+identifier)
		if collation != 0 {
			co, ok := in.Catalog.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(collation)})
			if !ok {
				return fmt.Errorf("stuntgres: missing element_types collation")
			}
			nco, ok := in.Catalog.Namespace(co.Namespace())
			if !ok {
				return fmt.Errorf("stuntgres: missing element_types collation namespace")
			}
			if nco.Name() != "pg_catalog" || co.Name() != "default" {
				text(11, database)
				text(12, nco.Name())
				text(13, co.Name())
			}
		}
		if err := wrapDomainCells(in.Source.Columns, row); err != nil {
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
		rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, address), false)
		return rights&mask != 0, err
	}
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.Relation:
			kind := ClassKind(in.Catalog, d)
			if kind != "r" && kind != "v" && kind != "f" && kind != "p" && kind != "c" {
				continue
			}
			if address != d.Address() || len(d.Columns) != len(d.Attributes) || d.RelationKind == catalog.CatalogRelation && len(d.ColumnMeta.CatalogAttributes) != len(d.Columns) {
				return nil, fmt.Errorf("stuntgres: invalid element_types relation")
			}
			ns, ok := in.Catalog.Namespace(d.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing element_types relation namespace")
			}
			objectType := "TABLE"
			ownerVisible := false
			if kind == "c" {
				objectType = "USER-DEFINED TYPE"
				typ, err := resolve(ids.OID(d.RowType), sqltype.Usage{})
				if err != nil {
					return nil, err
				}
				if !typ.SQLType.IsComposite() || typ.OwningRelation != address {
					return nil, fmt.Errorf("stuntgres: invalid element_types composite identity")
				}
				allowed, err := visible(typ.Address(), typ.Owner, authorization.UsagePrivilege)
				if err != nil {
					return nil, err
				}
				if !allowed {
					continue
				}
				ownerVisible = true
			} else {
				if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != in.Runtime.TemporaryOwner() {
					continue
				}
				var err error
				ownerVisible, err = in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), d.Owner)
				if err != nil {
					return nil, err
				}
			}
			for slot, column := range d.Columns {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				attribute := d.Attributes[slot]
				if _, dropped := d.ColumnMeta.Dropped[attribute]; dropped {
					continue
				}
				if attribute <= 0 || column.Usage.Type == nil {
					return nil, fmt.Errorf("stuntgres: invalid element_types attribute")
				}
				if !ownerVisible {
					target := address
					target.SubID = attribute
					rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, target), false)
					if err != nil {
						return nil, err
					}
					if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) == 0 {
						continue
					}
				}
				declared, err := resolve(ids.OID(column.Usage.Type.ID()), column.Usage)
				if err != nil {
					return nil, err
				}
				metadata := AttributeMetadata(d, slot)
				if err := emit(ns.Name(), d.Name(), objectType, int(attribute), declared, metadata.Collation); err != nil {
					return nil, err
				}
			}
		case *catalog.Type:
			if !d.SQLType.IsDomain() {
				continue
			}
			if address != d.Address() {
				return nil, fmt.Errorf("stuntgres: invalid element_types domain")
			}
			if err := in.Runtime.ValidateType(in.Catalog, d); err != nil {
				return nil, err
			}
			allowed, err := visible(address, d.Owner, authorization.UsagePrivilege)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			ns, ok := in.Catalog.Namespace(d.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing element_types domain namespace")
			}
			base := d.SQLType.DomainBase()
			declared, err := resolve(ids.OID(base.Type.ID()), base)
			if err != nil {
				return nil, err
			}
			if err := emit(ns.Name(), d.Name(), "DOMAIN", 1, declared, base.Collation.ID); err != nil {
				return nil, err
			}
		case *catalog.Procedure:
			if address != d.Address() {
				return nil, fmt.Errorf("stuntgres: invalid element_types routine")
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
				return nil, fmt.Errorf("stuntgres: invalid element_types routine kind")
			}
			ns, ok := in.Catalog.Namespace(d.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing element_types routine namespace")
			}
			identity := routineSpecificName(d.Name(), address.ObjectID)
			if err := VisitProcedureFormals(in.Context, d, sqltype.Value{}, func(f ProcedureFormal) error {
				declared, err := resolve(f.Type, f.Usage)
				if err != nil {
					return err
				}
				return emit(ns.Name(), identity, "ROUTINE", f.Index+1, declared, 0)
			}); err != nil {
				return nil, err
			}
			if d.RoutineKind != catalog.ProcedureRoutine {
				result, err := resolve(d.Result, sqltype.Usage{})
				if err != nil {
					return nil, err
				}
				if err := emit(ns.Name(), identity, "ROUTINE", 0, result, 0); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// Metadata admission is distinct from nominal declaration eligibility and codec
// admission. Validate every canonical array/domain hop before testing the leaf's
// existing metadata inventory, so an unrelated lookup-only type is absent while
// a malformed generation still fails. TypeFacts subsequently receives the
// original declaration: this walk never flattens an element_types row.
func infoElementTypesMetadataAdmitted(in Input, declared *catalog.Type) (bool, error) {
	for {
		if err := in.Context.Err(); err != nil {
			return false, err
		}
		var next sqltype.Usage
		switch typ := declared.SQLType; {
		case typ.IsArray():
			next.Type = typ.Element()
		case typ.IsDomain() && (typ.Concrete() || typ.IsUserDefined()):
			next = typ.DomainBase()
		default:
			return pgTypeAdmitted(in.Catalog, declared), nil
		}
		actual, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(next.Type.ID())})
		if !ok || actual.SQLType != next.Type {
			return false, fmt.Errorf("stuntgres: foreign element_types metadata generation")
		}
		if err := in.Runtime.ValidateType(in.Catalog, actual); err != nil {
			return false, err
		}
		declared = actual
	}
}
