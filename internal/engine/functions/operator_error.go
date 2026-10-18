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
