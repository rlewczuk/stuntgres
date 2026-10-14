package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

// ExclusionOperator binds the strict original-type equality, independently of
// IndexClass.Accepts: binary index input admission is not family membership.
type ExclusionOperator struct {
	Name        string
	Left, Right ID
	comparison  Comparison
	usage       Usage
}

func BindExclusionOperator(u Usage, class IndexClass, name string) (ExclusionOperator, error) {
	if u.Type == nil || !u.Type.Concrete() {
		return ExclusionOperator{}, fmt.Errorf("invalid exclusion operator input")
	}
	t := u.Type.BaseType()
	id := t.ID()
	operatorType := t.DiagnosticName()
	if id == VarChar {
		id, operatorType = Text, "text"
	}
	if id == Regclass || id == Regtype || id == Regrole || IsRegObjectAlias(id) {
		id, operatorType = Oid, "oid"
	}
	if t.IsEnum() {
		operatorType = "anyenum"
	}
	if name == "!=" {
		name = "<>"
	}
	signature := name + "(" + operatorType + "," + operatorType + ")"
	missing := func(coercion bool) error {
		message := "operator does not exist: " + operatorType + " " + name + " " + operatorType
		d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: message}
		if coercion {
			d.Message = "operator requires run-time type coercion: " + operatorType + " " + name + " " + operatorType
		} else {
			d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		}
		return d
	}
	comparisonOperator := name == "=" || name == "<>" || name == "<" || name == ">" || name == "<=" || name == ">="
	if !comparisonOperator {
		var arithmetic ArithmeticOperator
		known := true
		switch name {
		case "+":
			arithmetic = ArithmeticAdd
		case "-":
			arithmetic = ArithmeticSubtract
		case "*":
			arithmetic = ArithmeticMultiply
		case "/":
			arithmetic = ArithmeticDivide
		case "%":
			arithmetic = ArithmeticRemainder
		case "^":
			arithmetic = ArithmeticPower
		default:
			known = false
		}
		if !known {
			return ExclusionOperator{}, missing(false)
		}
		signature, err := ResolveArithmetic(arithmetic, t, t, false)
		if err != nil {
			return ExclusionOperator{}, missing(false)
		}
		if signature.Left != id || signature.Right != id {
			return ExclusionOperator{}, missing(true)
		}
	}
	comparison, err := BindOrdering(u)
	if err != nil && comparisonOperator {
		return ExclusionOperator{}, missing(false)
	}
	if name == "<" || name == ">" || name == "<=" || name == ">=" || name == "-" || name == "/" || name == "%" || name == "^" {
		return ExclusionOperator{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "operator " + signature + " is not commutative", Detail: "Only commutative operators can be used in exclusion constraints."}
	}
	defaultClass, exists := DefaultIndexClass(t)
	if class.IsHash() {
		defaultClass, exists = DefaultHashIndexClass(t)
	}
	member := exists && (class.Family == defaultClass.Family || id == Text && class.Family == "text_pattern_ops" || id == BPChar && class.Family == "bpchar_pattern_ops")
	if name != "=" || !member {
		return ExclusionOperator{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`operator %s is not a member of operator family "%s"`, signature, class.Family), Detail: "The exclusion operator must be related to the index operator class for the constraint."}
	}
	if err != nil {
		return ExclusionOperator{}, err
	}
	return ExclusionOperator{Name: name, Left: id, Right: id, comparison: comparison, usage: u}, nil
}

// MatchesUsage prevents a captured comparison from crossing descriptor generations.
func (p ExclusionOperator) MatchesUsage(u Usage) bool { return p.usage == u }

func (p ExclusionOperator) Equal(ctx context.Context, left, right Value) (bool, error) {
	if left.IsNull() || right.IsNull() {
		return false, nil
	}
	comparison, err := p.comparison.Compare(ctx, left, right, InputEnvironment{})
	return comparison == 0, err
}
