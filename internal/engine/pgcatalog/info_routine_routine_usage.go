package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoutineRoutineUsageOID is the native virtual relation identity, not a
// PostgreSQL allocator-dependent information-schema view OID.
const InfoRoutineRoutineUsageOID ids.OID = 16232

// captureInfoRoutineRoutineUsage reads the same hard-definition edges as
// pg_depend. Only the referenced owner's enabled role controls disclosure.
func captureInfoRoutineRoutineUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoRoutineRoutineUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: invalid routine_routine_usage source")
	}
	domain, domainOK := in.Catalog.LookupType(13248)
	base, baseOK := in.Catalog.LookupType(sqltype.Name)
	if !domainOK || !baseOK || !domain.IsDomain() || domain.BaseType() != base {
		return nil, fmt.Errorf("stuntgres: invalid routine_routine_usage identifier domain")
	}
	for _, column := range in.Source.Columns {
		if column.Usage != domain.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid routine_routine_usage identifier domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		source, ok := descriptor.(*catalog.Procedure)
		if !ok || source.RoutineKind != catalog.FunctionRoutine && source.RoutineKind != catalog.ProcedureRoutine {
			continue
		}
		var seen map[ids.OID]struct{}
		var prefix [4]sqltype.Value
		initialized := false
		var failure error
		catalog.VisitProcedureDefinitionDependencies(source, func(address ids.ObjectAddress) bool {
			if failure = in.Context.Err(); failure != nil {
				return false
			}
			if address.ClassID != ids.ProcedureClassID {
				return true
			}
			if address.SubID != 0 {
				failure = fmt.Errorf("stuntgres: invalid routine_routine_usage routine address")
				return false
			}
			target, ok := in.Catalog.Procedure(address)
			if !ok {
				failure = fmt.Errorf("stuntgres: missing routine_routine_usage dependency")
				return false
			}
			if target.RoutineKind != catalog.FunctionRoutine && target.RoutineKind != catalog.ProcedureRoutine || in.Runtime.Pinned(in.Catalog, address) {
				return true
			}
			if _, duplicate := seen[address.ObjectID]; duplicate {
				return true
			}
			if seen == nil {
				seen = make(map[ids.OID]struct{})
			}
			seen[address.ObjectID] = struct{}{}
			visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), target.Owner)
			if err != nil {
				failure = err
				return false
			}
			if !visible {
				return true
			}
			if !initialized {
				namespace, ok := in.Catalog.Namespace(source.Namespace())
				if !ok {
					failure = fmt.Errorf("stuntgres: missing routine_routine_usage source namespace")
					return false
				}
				database := in.Runtime.DatabaseName()
				for i, value := range [4]string{database, namespace.Name(), routineSpecificName(source.Name(), source.Address().ObjectID), database} {
					typ := in.Source.Columns[i].Usage.Type
					prefix[i], failure = typ.Wrap(sqltype.String(typ.BaseType(), value))
					if failure != nil {
						return false
					}
				}
				initialized = true
			}
			namespace, ok := in.Catalog.Namespace(target.Namespace())
			if !ok {
				failure = fmt.Errorf("stuntgres: missing routine_routine_usage target namespace")
				return false
			}
			row := make([]sqltype.Value, 6)
			copy(row, prefix[:])
			for i, value := range [2]string{namespace.Name(), routineSpecificName(target.Name(), target.Address().ObjectID)} {
				typ := in.Source.Columns[i+4].Usage.Type
				row[i+4], failure = typ.Wrap(sqltype.String(typ.BaseType(), value))
				if failure != nil {
					return false
				}
			}
			rows = append(rows, row)
			return true
		})
		if failure != nil {
			return nil, failure
		}
	}
	return rows, nil
}
