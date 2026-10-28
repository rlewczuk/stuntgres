package functions

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// OperatorError rewrites a 42883/42725 resolution failure as PostgreSQL's
// operator diagnostic for the operand types; other errors pass through.
func OperatorError(err error, left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42883" && d.Code != "42725" {
		return err
	}
	copy := *d
	name := func(t *sqltype.Descriptor) string {
		if t == nil {
			return "unknown"
		}
		return t.DiagnosticName()
	}
	if len(raw.Args) == 1 {
		copy.Message = fmt.Sprintf("operator does not exist: %s %s", qualifiedName(raw.Name), name(left))
		copy.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
	} else {
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", name(left), qualifiedName(raw.Name), name(right))
		copy.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	}
	if copy.Code == "42725" {
		copy.Message = strings.Replace(copy.Message, "operator does not exist:", "operator is not unique:", 1)
		copy.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
	}
	copy.Position = ""
	if raw.Pos.Character > 0 {
		copy.Position = strconv.Itoa(raw.Pos.Character)
	}
	return &copy
}

// ComparisonError is OperatorError with PostgreSQL's visibility-aware type
// names in both missing-operator and ambiguous-operator diagnostics.
func ComparisonError(b Binder, err error, left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	diagnostic := OperatorError(err, left, right, raw)
	if record, ok := diagnostic.(*sqlstate.Error); ok && (record.Code == "42883" || record.Code == "42725") {
		copy := *record
		problem := "operator does not exist"
		if record.Code == "42725" {
			problem = "operator is not unique"
		}
		copy.Message = fmt.Sprintf("%s: %s %s %s", problem, b.DiagnosticTypeName(left), qualifiedName(raw.Name), b.DiagnosticTypeName(right))
		return &copy
	}
	return diagnostic
}

// ArrayOperatorError is the 42883 of the array-shaped operators (membership,
// concatenation, ranges and JSONB): array operands are named by element type
// with a "[]" suffix.
func ArrayOperatorError(b Binder, left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	err := ComparisonError(b, sqlError("42883", "operator does not exist"), left, right, raw)
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", ArrayTypeName(b, left), qualifiedName(raw.Name), ArrayTypeName(b, right))
		return &copy
	}
	return err
}
