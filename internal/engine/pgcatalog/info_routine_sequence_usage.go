package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoRoutineSequenceUsageOID is the native virtual relation identity, not a
// PostgreSQL allocator-dependent information-schema view OID.
const InfoRoutineSequenceUsageOID ids.OID = 16205

// captureInfoRoutineSequenceUsage projects only direct stored sequence edges.
// PostgreSQL filters these rows by enabled sequence-owner roles, not by routine
// EXECUTE, schema USAGE, or sequence ACLs.
func captureInfoRoutineSequenceUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoRoutineSequenceUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 9 {
		return nil, fmt.Errorf("stuntgres: invalid routine_sequence_usage source")
	}
	for _, column := range in.Source.Columns {
		typ := column.Usage.Type
		if typ == nil || !typ.IsDomain() || typ.ID() != 13248 || typ.BaseType().ID() != sqltype.Name {
			return nil, fmt.Errorf("stuntgres: invalid routine_sequence_usage identifier domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		routine, ok := descriptor.(*catalog.Procedure)
		if !ok || routine.SQLBody == nil {
			continue
		}
		// Different live attributes remain distinct stored ObjectRefs but
		// contribute only one pair for their containing sequence.
		var seen map[ids.OID]struct{}
		initialized := false
		var prefix [7]sqltype.Value
		for _, dependency := range routine.SQLBody.Dependencies() {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			ref := dependency.Ref
			if ref.Address.ClassID != ids.RelationClassID {
				continue
			}
			if ref.Domain() != in.Ref.Domain() || ref.Epoch != in.Ref.Epoch {
				return nil, ids.ErrCatalogReplaced
			}
			address := ref.Address
			address.SubID = 0
			sequence, ok := in.Catalog.Relation(address)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing routine_sequence_usage dependency")
			}
			if sequence.RelationKind != catalog.SequenceRelation {
				continue
			}
			if ref.Address.SubID != 0 && !sequence.HasAttribute(ref.Address.SubID) {
				return nil, fmt.Errorf("stuntgres: invalid routine_sequence_usage sequence attribute")
			}
			if _, duplicate := seen[address.ObjectID]; duplicate {
				continue
			}
			visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), sequence.Owner)
			if err != nil {
				return nil, err
			}
			if !visible {
				continue
			}
			if !initialized {
				namespace, ok := in.Catalog.Namespace(routine.Namespace())
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing routine_sequence_usage routine namespace")
				}
				database := in.Runtime.DatabaseName()
				values := [7]string{database, namespace.Name(), routineSpecificName(routine.Name(), routine.Address().ObjectID), database, namespace.Name(), routine.Name(), database}
				for i, value := range values {
					typ := in.Source.Columns[i].Usage.Type
					prefix[i], err = typ.Wrap(sqltype.String(typ.BaseType(), value))
					if err != nil {
						return nil, err
					}
				}
				initialized = true
			}
			namespace, ok := in.Catalog.Namespace(sequence.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing routine_sequence_usage sequence namespace")
			}
			row := make([]sqltype.Value, 9)
			copy(row, prefix[:])
			for i, value := range [2]string{namespace.Name(), sequence.Name()} {
				typ := in.Source.Columns[i+7].Usage.Type
				row[i+7], err = typ.Wrap(sqltype.String(typ.BaseType(), value))
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
			if seen == nil {
				seen = make(map[ids.OID]struct{})
			}
			seen[address.ObjectID] = struct{}{}
		}
	}
	return rows, nil
}
