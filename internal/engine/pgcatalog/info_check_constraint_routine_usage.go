package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoCheckConstraintRoutineUsageOID is a native source identity, independent
// of PostgreSQL's allocator-dependent information-schema view OID.
const InfoCheckConstraintRoutineUsageOID ids.OID = 16228

func captureInfoCheckConstraintRoutineUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoCheckConstraintRoutineUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: invalid check_constraint_routine_usage source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	base, baseOK := in.Catalog.LookupType(sqltype.Name)
	if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base {
		return nil, fmt.Errorf("stuntgres: invalid check_constraint_routine_usage identifier domain")
	}
	for _, column := range in.Source.Columns {
		if column.Usage != domain.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid check_constraint_routine_usage source domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	var seen map[[6]string]struct{}
	database := in.Runtime.DatabaseName()
	// Each definition retains its owning namespace and constraint identity until
	// projection. Only the released six-column tuple is subject to DISTINCT.
	emit := func(namespace ids.ObjectAddress, oid ids.OID, name string, expression expr.Expr) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		if oid == 0 || name == "" {
			return fmt.Errorf("stuntgres: invalid check_constraint_routine_usage definition")
		}
		refs, err := in.Runtime.ExpressionReferences(in.Context, in.Catalog, expression)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := in.Context.Err(); err != nil {
				return err
			}
			if ref.ClassID != ids.ProcedureClassID {
				continue
			}
			routine, ok := in.Catalog.Get(ref).(*catalog.Procedure)
			if !ok {
				return fmt.Errorf("stuntgres: missing check_constraint_routine_usage routine")
			}
			visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), routine.Owner)
			if err != nil {
				return err
			}
			if !visible {
				continue
			}
			constraintSchema, ok := in.Catalog.Namespace(namespace)
			if !ok {
				return fmt.Errorf("stuntgres: missing check_constraint_routine_usage constraint namespace")
			}
			routineSchema, ok := in.Catalog.Namespace(routine.Namespace())
			if !ok {
				return fmt.Errorf("stuntgres: missing check_constraint_routine_usage routine namespace")
			}
			key := [6]string{database, constraintSchema.Name(), name, database, routineSchema.Name(), routineSpecificName(routine.Name(), ref.ObjectID)}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			row := make([]sqltype.Value, 6)
			for i, value := range key {
				row[i], err = domain.Wrap(sqltype.String(base, value))
				if err != nil {
					return err
				}
			}
			if seen == nil {
				seen = make(map[[6]string]struct{})
			}
			seen[key] = struct{}{}
			rows = append(rows, row)
		}
		return nil
	}
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.Relation:
			for i := range d.Constraints.Check {
				check := &d.Constraints.Check[i]
				if err := emit(d.Namespace(), check.OID, check.Name, check.Expression); err != nil {
					return nil, err
				}
			}
		case *catalog.Type:
			if !d.SQLType.IsDomain() {
				continue
			}
			for i := range d.DomainChecks {
				check := &d.DomainChecks[i]
				if err := emit(d.Namespace(), check.OID, check.Name, check.Expression); err != nil {
					return nil, err
				}
			}
		}
	}
	return rows, nil
}
