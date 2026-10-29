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

// InfoParametersOID is a native source identity, not a PostgreSQL allocation.
const InfoParametersOID ids.OID = 16238

var infoParametersColumns = []ColumnSpec{
	{"specific_catalog", 13248, true, 950, 0, -1},
	{"specific_schema", 13248, true, 950, 0, -1},
	{"specific_name", 13248, true, 950, 0, -1},
	{"ordinal_position", 13243, true, 0, 0, -1},
	{"parameter_mode", 13246, true, 950, 0, -1},
	{"is_result", 13256, true, 950, 0, -1},
	{"as_locator", 13256, true, 950, 0, -1},
	{"parameter_name", 13248, true, 950, 0, -1},
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
	{"parameter_default", 13246, true, 950, 0, -1},
}

func captureInfoParameters(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing parameters runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoParametersOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || in.TypeNamePath == nil || len(in.Source.Columns) != len(infoParametersColumns) || len(in.Source.Attributes) != len(infoParametersColumns) {
		return nil, fmt.Errorf("stuntgres: invalid parameters source")
	}
	for i, expected := range infoParametersColumns {
		column := in.Source.Columns[i]
		typ, ok := in.Catalog.LookupType(expected.Type)
		if !ok || !typ.IsDomain() || column.Name != expected.Name || column.Usage != typ.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != expected.Dimensions || column.OriginTable != uint32(InfoParametersOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid parameters source domain")
		}
		baseID := sqltype.VarChar
		switch expected.Type {
		case 13248:
			baseID = sqltype.Name
		case 13243:
			baseID = sqltype.Int4
		}
		base, ok := in.Catalog.LookupType(baseID)
		if !ok || typ.DomainBase().Type != base {
			return nil, fmt.Errorf("stuntgres: foreign parameters source base")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	columns := in.Source.Columns[:len(infoParametersColumns)]
	nulls, err := domainNulls(columns)
	if err != nil {
		return nil, err
	}
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Procedure)
		if !ok {
			continue
		}
		if address != d.Address() {
			return nil, fmt.Errorf("stuntgres: invalid parameters declaration")
		}
		owner, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), d.Owner)
		if err != nil {
			return nil, err
		}
		if !owner {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, address), false)
			if err != nil {
				return nil, err
			}
			if rights&authorization.ExecutePrivilege == 0 {
				continue
			}
		}
		namespace, ok := in.Catalog.Namespace(d.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing parameters namespace")
		}
		names, err := ProcedureNameProjection(in.Catalog, d)
		if err != nil {
			return nil, err
		}
		if owner {
			if d.SQL != nil {
				if d.LiteralDefaults != nil {
					return nil, fmt.Errorf("stuntgres: invalid checked default authority")
				}
				if d.SQLDefaults != nil {
					if err := d.SQLDefaults.ValidateDefinition(d); err != nil {
						return nil, err
					}
					if err := d.SQLDefaults.ValidateMetadata(in.Catalog); err != nil {
						return nil, err
					}
				}
			} else {
				count := int(d.Installed.Defaults)
				if d.Builtin != nil {
					count = int(d.Builtin.Defaults)
				}
				if d.LiteralDefaults.Len() != count || count == 0 && d.LiteralDefaults != nil || d.SQLDefaults != nil {
					return nil, fmt.Errorf("stuntgres: invalid literal default declaration")
				}
			}
		}
		identity := routineSpecificName(d.Name(), address.ObjectID)
		err = VisitProcedureFormals(in.Context, d, sqltype.Value{}, func(f ProcedureFormal) error {
			if f.Index >= names.count {
				return fmt.Errorf("stuntgres: invalid formal name layout")
			}
			declared, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: f.Type})
			if !ok {
				return fmt.Errorf("stuntgres: missing parameter type")
			}
			if err := in.Runtime.ValidateType(in.Catalog, declared); err != nil {
				return err
			}
			if f.Usage.Type != nil && f.Usage.Type != declared.SQLType {
				return fmt.Errorf("stuntgres: foreign parameter usage")
			}
			typeNamespace, ok := in.Catalog.Namespace(declared.Namespace())
			if !ok {
				return fmt.Errorf("stuntgres: missing parameter type namespace")
			}
			dataType, err := routineDataType(in, declared, typeNamespace)
			if err != nil {
				return err
			}
			row := slices.Clone(nulls)
			text := func(i int, value string) { row[i] = sqltype.String(in.Source.Columns[i].Usage.Type.BaseType(), value) }
			text(0, database)
			text(1, namespace.Name())
			text(2, identity)
			row[3], err = sqltype.Integer(in.Source.Columns[3].Usage.Type.BaseType(), int64(f.Index+1))
			if err != nil {
				return err
			}
			mode := "IN"
			switch f.Mode {
			case "o", "t":
				mode = "OUT"
			case "b":
				mode = "INOUT"
			}
			text(4, mode)
			text(5, "NO")
			text(6, "NO")
			if name := names.At(f.Index); name != "" {
				text(7, name)
			}
			text(8, dataType)
			text(23, database)
			text(24, typeNamespace.Name())
			text(25, declared.Name())
			text(30, strconv.Itoa(f.Index+1))
			// Redact before obtaining an expression, provider recipe or presentation.
			if owner && f.HasDefault {
				def, err := in.Runtime.RoutineDefault(in.Context, in.Catalog, d, f.Input)
				if err != nil {
					return err
				}
				text(31, def)
			}
			if err := wrapDomainCells(columns, row); err != nil {
				return err
			}
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
