package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// RowViolation is the first NOT NULL column or CHECK constraint a physical
// row violates. The caller renders it with its own disclosure detail.
type RowViolation struct {
	Relation *catalog.Relation
	Column   string                   // the NOT NULL column, when Check is nil
	Check    *catalog.CheckConstraint // the violated CHECK constraint
}

// Diagnostic is the 23502 or 23514 error for the violation.
func (v *RowViolation) Diagnostic(schema, detail string) error {
	if v.Check != nil {
		return CheckDetailError(schema, v.Relation.Name(), v.Check.Name, detail)
	}
	return NotNullDetailError(schema, v.Relation.Name(), v.Column, detail)
}

// PhysicalRowViolation checks NOT NULL columns, then CHECK constraints, for
// ordinary insertion and isolated native replacement. It does not mutate the
// row and returns nil when the row is valid.
func PhysicalRowViolation(relation *catalog.Relation, row []sqltype.Value, evaluation *exec.Context) (*RowViolation, error) {
	for j, column := range relation.Columns {
		if column.Nullable {
			continue
		}
		value, err := LogicalColumnValue(relation, j, row, evaluation)
		if err != nil {
			return nil, err
		}
		if value.IsNull() {
			return &RowViolation{Relation: relation, Column: column.Name}, nil
		}
	}
	if len(relation.Constraints.Check) != 0 {
		constraint, err := ValidateCheckRow(relation, row, evaluation)
		if err != nil {
			return nil, err
		}
		if constraint != nil {
			return &RowViolation{Relation: relation, Check: constraint}, nil
		}
	}
	return nil, nil
}

// NotNullDetailError is the 23502 not-null violation of column.
func NotNullDetailError(schema, table, column, detail string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
		Message: fmt.Sprintf("null value in column \"%s\" of relation \"%s\" violates not-null constraint", column, table),
		Schema:  schema, Table: table, Column: column, Detail: detail,
	}
}
