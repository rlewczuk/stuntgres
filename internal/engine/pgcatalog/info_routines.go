package pgcatalog

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoutinesOID is a native relation identity, not a PostgreSQL view OID.
const InfoRoutinesOID ids.OID = 16219

func captureInfoRoutines(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing routines runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoRoutinesOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || !in.Source.VirtualView || in.TypeNamePath == nil || len(in.Source.Columns) != len(infoRoutinesColumns) || len(in.Source.Attributes) != len(infoRoutinesColumns) {
		return nil, fmt.Errorf("stuntgres: invalid routines source")
	}
	for i, expected := range infoRoutinesColumns {
		column := in.Source.Columns[i]
		typ, ok := in.Catalog.LookupType(expected.Type)
		if !ok || !typ.IsDomain() || column.Name != expected.Name || column.Usage != typ.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != expected.Dimensions || column.OriginTable != uint32(InfoRoutinesOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid routines source domain")
		}
		baseID := sqltype.VarChar
		switch expected.Type {
		case 13248:
			baseID = sqltype.Name
		case 13243:
			baseID = sqltype.Int4
		case 13254:
			baseID = sqltype.Timestamptz
		}
		base, ok := in.Catalog.LookupType(baseID)
		if !ok || typ.DomainBase().Type != base {
			return nil, fmt.Errorf("stuntgres: foreign routines source base")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	columns := in.Source.Columns[:len(infoRoutinesColumns)]
	nulls, err := domainNulls(columns)
	if err != nil {
		return nil, err
	}
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		routine, ok := descriptor.(*catalog.Procedure)
		if !ok {
			continue
		}
		if address != routine.Address() || !routine.Display.Valid() {
			return nil, fmt.Errorf("stuntgres: invalid routines declaration")
		}
		owner, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), routine.Owner)
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
		namespace, ok := in.Catalog.Namespace(routine.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing routines namespace")
		}
		cell := func(i int, value string) func() (sqltype.Value, error) {
			return func() (sqltype.Value, error) { return domainText(columns[i], value) }
		}
		if in.Keep.Rejects(InfoRoutinesSpecificSchemaColumn, cell(InfoRoutinesSpecificSchemaColumn, namespace.Name())) ||
			in.Keep.Rejects(InfoRoutinesSpecificNameColumn, cell(InfoRoutinesSpecificNameColumn, routineSpecificName(routine.Name(), address.ObjectID))) ||
			in.Keep.Rejects(InfoRoutinesSchemaColumn, cell(InfoRoutinesSchemaColumn, namespace.Name())) {
			continue
		}
		row := slices.Clone(nulls)
		text := func(i int, value string) {
			row[i] = sqltype.String(in.Source.Columns[i].Usage.Type.BaseType(), value)
		}
		text(0, database)
		text(1, namespace.Name())
		text(2, routineSpecificName(routine.Name(), address.ObjectID))
		text(3, database)
		text(4, namespace.Name())
		text(5, routine.Name())
		switch routine.RoutineKind {
		case catalog.FunctionRoutine:
			text(6, "FUNCTION")
		case catalog.ProcedureRoutine:
			text(6, "PROCEDURE")
		case catalog.AggregateRoutine, catalog.WindowRoutine:
			// Released prokind a/w has no routine_type CASE arm.
		default:
			return nil, fmt.Errorf("stuntgres: invalid routines kind")
		}
		if routine.RoutineKind != catalog.ProcedureRoutine {
			result, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: routine.Result})
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing routines result type")
			}
			resultNamespace, ok := in.Catalog.Namespace(result.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing routines result namespace")
			}
			if err := in.Runtime.ValidateType(in.Catalog, result); err != nil {
				return nil, err
			}
			dataType, err := routineDataType(in, result, resultNamespace)
			if err != nil {
				return nil, err
			}
			text(13, dataType)
			text(28, database)
			text(29, resultNamespace.Name())
			text(30, result.Name())
			text(35, "0")
			text(43, "NO")
			if routine.Strict {
				text(43, "YES")
			}
		}
		text(36, "EXTERNAL")
		if routine.Display.Language == catalog.SQLRoutineLanguage {
			text(36, "SQL")
		}
		if owner {
			text(37, routine.Display.Source)
		}
		if routine.Display.Language == catalog.CRoutineLanguage {
			text(38, routine.Display.Source)
		}
		text(39, routine.Display.Language.String())
		text(40, "GENERAL")
		text(41, "NO")
		if routine.Volatility == expr.Immutable {
			text(41, "YES")
		}
		text(42, "MODIFIES")
		text(45, "YES")
		row[46], err = sqltype.Integer(in.Source.Columns[46].Usage.Type.BaseType(), 0)
		if err != nil {
			return nil, err
		}
		text(49, "INVOKER")
		if routine.SecurityDefiner {
			text(49, "DEFINER")
		}
		text(53, "NO")
		text(57, "NO")
		if err := wrapDomainCells(columns, row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
