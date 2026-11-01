package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoViewRoutineUsageOID is the native virtual relation identity, independent
// of PostgreSQL's allocator-dependent information-schema view OID.
const InfoViewRoutineUsageOID ids.OID = 16255

// captureInfoViewRoutineUsage projects direct stored SELECT-rule routine edges.
// Only enabled ownership of the referenced routine controls disclosure.
func captureInfoViewRoutineUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoViewRoutineUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: invalid view_routine_usage source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	base, baseOK := in.Catalog.LookupType(sqltype.Name)
	if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base {
		return nil, fmt.Errorf("stuntgres: invalid view_routine_usage identifier domain")
	}
	for _, column := range in.Source.Columns {
		if column.Usage != domain.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid view_routine_usage source domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		view, ok := descriptor.(*catalog.Relation)
		if !ok || view.RelationKind != catalog.ViewRelation || view.View == nil {
			continue
		}
		references, err := in.Runtime.ViewReferences(in.Context, in.Catalog, view)
		if err != nil {
			return nil, err
		}
		var prefix [4]sqltype.Value
		initialized := false
		for _, reference := range references {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			if reference.ClassID != ids.ProcedureClassID {
				continue
			}
			routine, ok := in.Catalog.Get(reference).(*catalog.Procedure)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing view_routine_usage routine")
			}
			visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), routine.Owner)
			if err != nil {
				return nil, err
			}
			if !visible {
				continue
			}
			if !initialized {
				viewSchema, ok := in.Catalog.Namespace(view.Namespace())
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing view_routine_usage view namespace")
				}
				for i, text := range [4]string{database, viewSchema.Name(), view.Name(), database} {
					prefix[i], err = domain.Wrap(sqltype.String(base, text))
					if err != nil {
						return nil, err
					}
				}
				initialized = true
			}
			routineSchema, ok := in.Catalog.Namespace(routine.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing view_routine_usage routine namespace")
			}
			// QueryDependRefs already deduplicates complete routine addresses.
			// Each view/routine pair therefore yields exactly one DISTINCT tuple.
			row := make([]sqltype.Value, 6)
			copy(row, prefix[:])
			for i, text := range [2]string{routineSchema.Name(), routineSpecificName(routine.Name(), reference.ObjectID)} {
				row[i+4], err = domain.Wrap(sqltype.String(base, text))
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
