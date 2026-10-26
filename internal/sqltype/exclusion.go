package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

// ExclusionOperator binds a strict predicate to its original stored types.
// Binary index admission is independent of actual operator family membership.
type ExclusionOperator struct {
	Name        string
	Left, Right ID
	// Canonical inputs come from the admitted index class, independently of
	// declaration/diagnostic types and the original stored-value comparison.
	operatorInputs [2]ID
	comparison     Comparison
	usage          Usage
	rangePlan      RangeOperatorPlan
	index          IndexComparison
	member         uint32
	pointPlan      PointPlan
	polygonPlan    PolygonPlan
	circlePlan     CirclePlan
	geometryPlan   GeometryRelationPlan
	gistPredicate  bool
}

// OperatorInputs identifies the released signature without retagging values.
func (p ExclusionOperator) OperatorInputs() ([2]ID, error) {
	if p.operatorInputs[0] == 0 || p.operatorInputs[1] == 0 {
		return [2]ID{}, fmt.Errorf("missing exclusion operator inputs")
	}
	return p.operatorInputs, nil
}

func (p ExclusionOperator) IsEquality() bool { return p.Name == "=" }

func BindExclusionOperator(u Usage, class IndexClass, name string, member uint32) (ExclusionOperator, error) {
	if u.Type == nil || !u.Type.Concrete() {
		return ExclusionOperator{}, fmt.Errorf("invalid exclusion operator input")
	}
	t := u.Type.BaseType()
	id := t.ID()
	if class.IsGiST() {
		if member == 0 {
			return ExclusionOperator{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`operator %s(%s,%s) is not a member of operator family "%s"`, name, t.DiagnosticName(), t.DiagnosticName(), class.Family), Detail: "The exclusion operator must be related to the index operator class for the constraint."}
		}
		p, err := bindGiSTExclusion(u, class, name)
		p.member = member
		return p, err
	}
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
	var comparison IndexComparison
	var err error
	if class.IsHash() {
		var hash IndexHash
		hash, err = BindIndexHash(u, class)
		comparison = hash.Comparison()
	} else {
		comparison, err = BindIndexOrdering(u, class)
	}
	if err != nil && comparisonOperator {
		return ExclusionOperator{}, missing(false)
	}
	if name == "<" || name == ">" || name == "<=" || name == ">=" || name == "-" || name == "/" || name == "%" || name == "^" {
		return ExclusionOperator{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "operator " + signature + " is not commutative", Detail: "Only commutative operators can be used in exclusion constraints."}
	}
	if name != "=" || member == 0 {
		return ExclusionOperator{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`operator %s is not a member of operator family "%s"`, signature, class.Family), Detail: "The exclusion operator must be related to the index operator class for the constraint."}
	}
	if err != nil {
		return ExclusionOperator{}, err
	}
	input := class.input
	if class.enum {
		input = 3500 // Released polymorphic anyenum operator input.
	}
	if input == 0 {
		return ExclusionOperator{}, fmt.Errorf("missing exclusion class input")
	}
	return ExclusionOperator{Name: name, Left: id, Right: id, operatorInputs: [2]ID{input, input}, index: comparison, member: member, usage: u}, nil
}

// MatchesUsage prevents a captured comparison from crossing descriptor generations.
func (p ExclusionOperator) MatchesUsage(u Usage) bool { return p.usage == u }

func (p ExclusionOperator) Matches(ctx context.Context, left, right Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if left.IsNull() || right.IsNull() {
		return false, nil
	}
	if p.gistPredicate {
		if left.typ != p.usage.Type || right.typ != p.usage.Type {
			return false, fmt.Errorf("foreign GiST exclusion value")
		}
		values := [2]Value{left, right}
		// Domains keep stored identity; owning scalar plans use their base type.
		values[0].typ, values[1].typ = p.usage.Type.BaseType(), p.usage.Type.BaseType()
		var value Value
		var err error
		switch {
		case p.pointPlan.input != nil:
			value, err = p.pointPlan.Evaluate(ctx, values)
		case p.polygonPlan.result != nil:
			value, err = p.polygonPlan.Evaluate(ctx, values)
		case p.circlePlan.input != nil:
			value, err = p.circlePlan.Evaluate(ctx, values)
		case p.geometryPlan.result != nil:
			value, err = p.geometryPlan.Evaluate(ctx, values)
		case p.rangePlan.result != nil:
			value, err = p.rangePlan.Evaluate(ctx, values)
		case values[0].typ.id == Box:
			return boxSame(values[0], values[1])
		default:
			return false, fmt.Errorf("missing GiST exclusion predicate")
		}
		if err != nil {
			return false, err
		}
		return value.Boolean()
	}
	if p.Name == "&&" {
		if left.typ != p.usage.Type || right.typ != p.usage.Type {
			return false, fmt.Errorf("foreign exclusion network value")
		}
		a, b := left.network, right.network
		a.prefix, b.prefix = min(a.prefix, b.prefix), min(a.prefix, b.prefix)
		return a.family == b.family && a.masked() == b.masked(), nil
	}
	if p.index.comparison.left != nil {
		comparison, err := p.index.Compare(ctx, left, right, InputEnvironment{})
		return comparison == 0, err
	}
	comparison, err := p.comparison.Compare(ctx, left, right, InputEnvironment{})
	if p.Name == "<>" {
		return comparison != 0, err
	}
	return comparison == 0, err
}
