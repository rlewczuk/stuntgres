package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type binaryExpression struct {
	expressionInfo
	plan sqltype.BinaryPlan
	args []BoundExpr
}

func (e binaryExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.args {
		if randomLiteralNull(arg) {
			return sqltype.Null(e.usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.plan.Evaluate(c.Context, values[0], values[1])
}

func binaryTextType(d *sqltype.Descriptor) bool {
	if d == nil {
		return true
	}
	switch d.ID() {
	case sqltype.Text, sqltype.VarChar, sqltype.BPChar, sqltype.Name, sqltype.Char:
		return true
	}
	return false
}

func (b *expressionBinder) bindBinaryFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operands []comparisonOperand
	var names []string
	if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.comparisonOperand(r)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = d.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (BoundExpr, error) { return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	if !visible || raw.Variadic {
		return missing()
	}
	arity := 2
	if name == "length" || name == "octet_length" || name == "bit_length" {
		arity = 1
	}
	if name == "length" && len(operands) == 2 {
		source, encoding := operands[0].descriptor(), operands[1].descriptor()
		if source == nil || source.ID() == sqltype.Bytea {
			// Recognize only the real excluded bytea/name candidate, including
			// unknown inputs and the existing implicit character-to-NAME edges.
			var conversionErr error
			if encoding != nil {
				_, conversionErr = sqltype.PlanCoercion(operands[1].node.ResultUsage(), b.typ(sqltype.Name).Usage(), sqltype.Implicit)
			}
			if conversionErr == nil {
				return nil, expressionError("0A000", "encoding-aware length is unsupported", raw.Pos)
			}
		}
	}
	text := len(operands) == arity || (arity == 2 && len(operands) == 1)
	for _, o := range operands {
		text = text && binaryTextType(o.descriptor())
	}
	if text {
		return nil, expressionError("0A000", "text overloads of "+name+" are unsupported", raw.Pos)
	}
	if len(operands) != arity {
		return missing()
	}
	known := false
	for _, o := range operands {
		if d := o.descriptor(); d != nil {
			if d.ID() != sqltype.Bytea {
				return missing()
			}
			known = true
		}
	}
	if !known {
		return missing()
	}
	return b.finishBinary(name, raw.Pos, operands)
}

func (b *expressionBinder) bindBinaryConcat(raw parser.OperatorExpr) (BoundExpr, error) {
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if len(raw.Args) != 2 {
		return nil, fmt.Errorf("stuntgres: invalid binary concatenation arity")
	}
	operands := make([]comparisonOperand, 2)
	for i, r := range raw.Args {
		operands[i], err = b.comparisonOperand(r)
		if err != nil {
			return nil, err
		}
	}
	lt, rt := operands[0].descriptor(), operands[1].descriptor()
	missing := func() (BoundExpr, error) {
		return nil, operatorExpressionError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
	}
	if !visible {
		return missing()
	}
	// PostgreSQL's actual text/anynonarray siblings must not be reported absent.
	if (lt != nil && binaryTextType(lt)) || (rt != nil && binaryTextType(rt)) || (lt == nil && (rt == nil || rt.ID() != sqltype.Bytea)) || (rt == nil && lt.ID() != sqltype.Bytea) {
		return nil, expressionError("0A000", "text overloads of || are unsupported", raw.Pos)
	}
	for _, d := range []*sqltype.Descriptor{lt, rt} {
		if d != nil && d.ID() != sqltype.Bytea {
			return missing()
		}
	}
	return b.finishBinary("||", raw.Pos, operands)
}

func (b *expressionBinder) finishBinary(name string, pos parser.Position, operands []comparisonOperand) (BoundExpr, error) {
	args := make([]BoundExpr, len(operands))
	var usages [2]sqltype.Usage
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		arg, err := b.finalizeOperand(o, b.typ(sqltype.Bytea))
		if err != nil {
			return nil, err
		}
		args[i] = arg
		usages[i] = arg.ResultUsage()
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	id := sqltype.Bytea
	if len(operands) == 1 {
		id = sqltype.Int4
	}
	usage := b.typ(id).Usage()
	plan, err := sqltype.BindBinary(name, usage, usages[:len(operands)]...)
	if err != nil {
		return nil, err
	}
	return binaryExpression{b.info(usage, pos, properties), plan, args}, nil
}
