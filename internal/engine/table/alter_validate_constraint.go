package table

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// AlterConstraintValidation is ephemeral scan intent, never catalog authority.
// Stable identities survive later candidate detachment and metadata transfer.
type AlterConstraintValidation struct {
	OID        ids.OID
	Check      *catalog.CheckConstraint
	NotNull    *catalog.NotNullConstraint
	ForeignKey *catalog.ForeignKeyConstraint
}

// SelectAlterConstraintValidation searches every constraint kind before deciding
// whether validation is a no-op. It does not bind, evaluate, or mutate anything.
func SelectAlterConstraintValidation(d *catalog.Relation, name string) (AlterConstraintValidation, error) {
	for i := range d.Constraints.Check {
		c := &d.Constraints.Check[i]
		if c.Name == name {
			return AlterConstraintValidation{OID: c.OID, Check: c}, nil
		}
	}
	for i := range d.Constraints.NotNull {
		c := &d.Constraints.NotNull[i]
		if c.Name == name {
			return AlterConstraintValidation{OID: c.OID, NotNull: c}, nil
		}
	}
	for i := range d.Constraints.ForeignKeys {
		c := &d.Constraints.ForeignKeys[i]
		if c.Name == name {
			return AlterConstraintValidation{OID: c.OID, ForeignKey: c}, nil
		}
	}
	if catalog.RelationConstraintNameExists(d, name) {
		return AlterConstraintValidation{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot validate constraint "%s" of relation "%s"`, name, d.Name()), Detail: "This operation is not supported for this type of constraint."}
	}
	return AlterConstraintValidation{}, sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, name, d.Name()))
}

func (v AlterConstraintValidation) Invalid() bool {
	return v.Check != nil && v.Check.NotValid || v.NotNull != nil && v.NotNull.NotValid || v.ForeignKey != nil && v.ForeignKey.NotValid
}

func (v AlterConstraintValidation) Recursive() bool {
	return v.Check != nil && v.Check.NotValid && !v.Check.NoInherit || v.NotNull != nil && v.NotNull.NotValid && !v.NotNull.NoInherit
}

// ValidateConstraintCandidate promotes only the selected immutable fact. An
// already-valid fact returns exactly its predecessor, without a revision change.
func ValidateConstraintCandidate(old *catalog.Relation, name string) (*catalog.Relation, AlterConstraintValidation, error) {
	v, err := SelectAlterConstraintValidation(old, name)
	if err == nil && (v.Check != nil && v.Check.NotEnforced || v.ForeignKey != nil && v.ForeignKey.NotEnforced) {
		return old, v, sqlError("55000", "cannot validate NOT ENFORCED constraint")
	}
	if err != nil || !v.Invalid() {
		return old, v, err
	}
	next := *old
	switch {
	case v.Check != nil:
		next.Constraints.Check = slices.Clone(old.Constraints.Check)
		for i := range next.Constraints.Check {
			if next.Constraints.Check[i].OID == v.OID && next.Constraints.Check[i].Name == name {
				next.Constraints.Check[i].NotValid = false
				break
			}
		}
	case v.NotNull != nil:
		next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
		for i := range next.Constraints.NotNull {
			if next.Constraints.NotNull[i].OID == v.OID && next.Constraints.NotNull[i].Name == name {
				next.Constraints.NotNull[i].NotValid = false
				break
			}
		}
	case v.ForeignKey != nil:
		next.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
		for i := range next.Constraints.ForeignKeys {
			if next.Constraints.ForeignKeys[i].OID == v.OID && next.Constraints.ForeignKeys[i].Name == name {
				next.Constraints.ForeignKeys[i].NotValid = false
				break
			}
		}
	}
	return &next, v, nil
}

// ValidationOnly marks the narrow recursive authority exception. Root and direct
// calls always retain their normal owner checks.
func ValidationOnly(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, a := range actions {
		if a.Action != parser.ValidateConstraint {
			return false
		}
	}
	return true
}

// ValidationChildAction maps NN by logical column name, not parent attnum or
// constraint name. CHECK retains its name. Provenance is not modified.
func ValidationChildAction(old *catalog.Relation, a parser.AlterTableAction) (parser.AlterTableAction, error) {
	if a.Column.Text == "" {
		return a, nil
	}
	if slot, ok := old.Index[a.Column.Text]; ok {
		for _, c := range old.Constraints.NotNull {
			if c.Attribute == old.Attributes[slot] {
				a.Constraint.Text = c.Name
				return a, nil
			}
		}
	}
	return a, sqlError("XX000", fmt.Sprintf(`cache lookup failed for not-null constraint on column "%s" of relation "%s"`, a.Column.Text, old.Name()))
}
