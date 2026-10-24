package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/unicode16"
)

// CitextComparison is scalar predicate authority, not ordering or hash authority.
// Folding uses database C ctype independently of the input ordering collation.
type CitextComparison struct {
	input, result *Descriptor
	operator      ComparisonOperator
	collation     CollationRef
	indeterminate bool
}

func BindCitextComparison(operator ComparisonOperator, left, right, result *Descriptor, collation CollationRef, indeterminate bool) (CitextComparison, error) {
	if operator > CompareGreaterEqual || !left.IsCitext() || left != right || !result.Concrete() || result.ID() != Bool || !indeterminate && !collation.Valid() {
		return CitextComparison{}, fmt.Errorf("invalid citext comparison")
	}
	return CitextComparison{left, result, operator, collation, indeterminate}, nil
}

func (p CitextComparison) Evaluate(ctx context.Context, a, b Value) (Value, error) {
	if !p.input.IsCitext() || a.typ != p.input || b.typ != p.input || !p.result.Concrete() || p.operator > CompareGreaterEqual {
		return Value{}, fmt.Errorf("invalid citext comparison values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.result), nil
	}
	if p.operator >= CompareLess && p.indeterminate {
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
	}
	left, err := unicode16.ConvertCase(ctx, a.text, unicode16.Lower, unicode16.ASCII)
	if err != nil {
		return Value{}, err
	}
	right, err := unicode16.ConvertCase(ctx, b.text, unicode16.Lower, unicode16.ASCII)
	if err != nil {
		return Value{}, err
	}
	if p.operator == CompareEqual || p.operator == CompareNotEqual {
		equal, err := groupingTextEqual(ctx, left, right)
		if err != nil {
			return Value{}, err
		}
		if p.operator == CompareNotEqual {
			equal = !equal
		}
		return Boolean(p.result, equal), nil
	}
	n, err := p.collation.CompareText(ctx, left, right)
	if err != nil {
		return Value{}, err
	}
	var truth bool
	switch p.operator {
	case CompareLess:
		truth = n < 0
	case CompareLessEqual:
		truth = n <= 0
	case CompareGreater:
		truth = n > 0
	case CompareGreaterEqual:
		truth = n >= 0
	}
	return Boolean(p.result, truth), nil
}
