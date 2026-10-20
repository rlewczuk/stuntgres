package analyze

import (
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ArrayMembershipName reports whether name is an array containment or overlap
// operator.
func ArrayMembershipName(name string) bool { return name == "@>" || name == "<@" || name == "&&" }

// Extrema retain ordering authority separately from scalar equality. Identifier
// types without ordering remain describable, but never acquire a fake comparator.
func BindExtremaComparison(usage sqltype.Usage, indeterminate bool) (sqltype.Comparison, bool, error) {
	// Array SQL comparison does not expand the independently bounded extrema
	// consumer. Preserve its previous analysis failure, including domains.
	if usage.Type.BaseType().IsArray() {
		return sqltype.Comparison{}, false, sqlError("42883", "incompatible comparison operands")
	}
	if usage.Type.BaseType().IsEnum() || sqltype.IsTransactionIdentifier(usage.Type.BaseType().ID()) {
		comparison, err := sqltype.BindOrdering(usage)
		if err != nil && (usage.Type.BaseType().ID() == sqltype.Xid || usage.Type.BaseType().ID() == sqltype.Cid) {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42883" {
				return sqltype.Comparison{}, true, nil
			}
		}
		return comparison, false, err
	}
	comparison, err := sqltype.BindComparison(usage.Type, usage.Type, usage.Collation, indeterminate)
	return comparison, false, err
}

// This is a dependency/persistence restriction, not a data codec restriction.
// Opaque array payloads, domains, runtime casts and typed NULL are not scalar
// REGROLE Const nodes. Their ordinary expression children are checked by callers.
func ValidateRegroleStoredLiteral(value sqltype.Value) error {
	if value.Type().ID() == sqltype.Regrole && !value.IsNull() {
		return sqlError("0A000", "constant of the type regrole cannot be used here")
	}
	return nil
}
