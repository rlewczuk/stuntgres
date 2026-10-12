package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type datetimeAgeFiniteExpression struct {
	expressionInfo
	plan  sqltype.DatetimeAgeFinitePlan
	args  [2]BoundExpr
	count int
}

func (e datetimeAgeFiniteExpression) children() []BoundExpr { return e.args[:e.count] }
func (e datetimeAgeFiniteExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.children() {
		if randomLiteralNull(arg) {
			return sqltype.Null(e.usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.children() {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.plan.Evaluate(c.Context, values[:e.count], c.Input)
}

// The candidates are only this PG18 function family's homogeneous signatures.
// XID is diagnostic-only: it has no native descriptor or executable overload.
const ageDiagnosticXid sqltype.ID = 28

func ageFiniteCategory(id sqltype.ID) byte {
	switch id {
	case sqltype.Date, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Time:
		return 'D'
	case sqltype.Interval:
		return 'T'
	default:
		return 'U'
	}
}
func ageFinitePreferred(id sqltype.ID) bool {
	return id == sqltype.Timestamptz || id == sqltype.Interval
}

func (b *expressionBinder) bindDatetimeAgeFinite(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]comparisonOperand
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if !visible || raw.Variadic || len(operands) < 1 || len(operands) > 2 || name == "isfinite" && len(operands) != 1 {
		return signature()
	}
	candidates := [4]sqltype.ID{sqltype.Timestamp, sqltype.Timestamptz}
	n := 2
	if name == "isfinite" {
		candidates, n = [4]sqltype.ID{sqltype.Date, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval}, 4
	} else if len(operands) == 1 {
		candidates[2], n = ageDiagnosticXid, 3
	}
	var viable [4]bool
	bestExact, bestPreferred := -1, -1
	for j, id := range candidates[:n] {
		ok, exact, preferred := true, 0, 0
		for _, o := range operands {
			d := o.descriptor()
			if d == nil {
				continue
			}
			source := d.BaseType().ID()
			if source == id {
				exact++
				continue
			}
			if id == ageDiagnosticXid {
				ok = false
				break
			}
			// PG has this implicit cast, but native deliberately does not.
			if source == sqltype.Time && id == sqltype.Interval {
				continue
			}
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(id).Usage(), sqltype.Implicit); err != nil {
				ok = false
				break
			}
			if ageFiniteCategory(source) == ageFiniteCategory(id) && ageFinitePreferred(id) {
				preferred++
			}
		}
		if !ok {
			continue
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			viable = [4]bool{}
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			viable[j] = true
		}
	}
	// UNKNOWN arguments require a single category. Within that category PG's
	// preferred input wins; no string category exists in this signature family.
	for _, o := range operands {
		if o.descriptor() != nil {
			continue
		}
		category, preferred, conflict := byte(0), false, false
		for j, id := range candidates[:n] {
			if !viable[j] {
				continue
			}
			cat := ageFiniteCategory(id)
			if category != 0 && category != cat {
				conflict = true
			}
			category = cat
			preferred = preferred || ageFinitePreferred(id)
		}
		if conflict {
			break
		}
		if preferred {
			for j, id := range candidates[:n] {
				if viable[j] && !ageFinitePreferred(id) {
					viable[j] = false
				}
			}
		}
	}
	selected, matches := sqltype.ID(0), 0
	for j, id := range candidates[:n] {
		if viable[j] {
			selected, matches = id, matches+1
		}
	}
	if matches == 0 {
		return signature()
	}
	if matches > 1 {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o)
		}
		err := expressionError("42725", fmt.Sprintf("function %s(%s) is not unique", objectNameText(raw.Name), strings.Join(names, ", ")), raw.Pos).(*sqlstate.Error)
		err.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, err
	}
	if selected == sqltype.Timestamptz {
		return nil, expressionError("0A000", name+" with time zone is unsupported", raw.Pos)
	}
	if selected == sqltype.Interval && operands[0].descriptor() != nil && operands[0].descriptor().BaseType().ID() == sqltype.Time {
		return nil, expressionError("0A000", "time to interval conversion is unsupported", raw.Pos)
	}
	e := datetimeAgeFiniteExpression{count: len(operands)}
	properties := ExprProperties{Volatility: Immutable}
	if name == "age" && e.count == 1 {
		properties.Volatility = Stable
	}
	target := b.typ(selected)
	for i, o := range operands {
		arg, err := b.finalizeOperand(o, target)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target.Usage() {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			arg = coercionExpression{b.info(target.Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit}
		}
		e.args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	result := sqltype.Interval
	if name == "isfinite" {
		result = sqltype.Bool
	}
	e.expressionInfo = b.info(b.typ(result).Usage(), raw.Pos, properties)
	plan, err := sqltype.BindDatetimeAgeFinite(name, target, e.usage.Type, b.typ(sqltype.Date), e.count)
	if err != nil {
		return nil, err
	}
	e.plan = plan
	return e, nil
}
