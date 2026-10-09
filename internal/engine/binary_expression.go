package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type binaryExpression struct {
	expressionInfo
	plan   sqltype.BinaryPlan
	args   []BoundExpr
	syntax parser.FunctionSyntax
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
	var values [4]sqltype.Value
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.plan.Evaluate(c.Context, values[:len(e.args)]...)
}

func (n binaryExpression) validateSyntax() error {
	if n.syntax == parser.OrdinaryFunctionSyntax {
		return nil
	}
	if n.syntax == parser.SQLFunctionSyntax {
		switch n.plan.Name() {
		case "overlay":
			if len(n.args) == 3 || len(n.args) == 4 {
				return nil
			}
		case "substring":
			if len(n.args) == 2 || len(n.args) == 3 {
				return nil
			}
		case "position", "btrim", "ltrim", "rtrim":
			if len(n.args) == 2 {
				return nil
			}
		}
	}
	return fmt.Errorf("stuntgres: invalid stored binary syntax")
}

func binaryTextType(d *sqltype.Descriptor) bool {
	if d == nil {
		return true
	}
	switch d.BaseType().ID() {
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
	hash := false
	switch name {
	case "crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512":
		hash = true
	}
	if hash {
		if !visible || len(operands) != 1 {
			return missing()
		}
		d := operands[0].descriptor()
		// Unknown MD5 belongs to the excluded TEXT overload in PostgreSQL.
		// CRC/SHA have a unique BYTEA signature and may infer it here.
		if (d == nil && name == "md5") || (d != nil && d.ID() != sqltype.Bytea) {
			return missing()
		}
		return b.finishBinary(name, raw.Pos, operands, raw.Syntax)
	}
	// PostgreSQL accepts VARIADIC spelling for these fixed scalar signatures;
	// it does not introduce an array argument or a variadic execution plan.
	selectedSlice := name == "overlay" || name == "position" || name == "substring" || name == "substr"
	if !visible || (raw.Variadic && !selectedSlice) {
		return missing()
	}
	arity := 2
	switch name {
	case "length", "octet_length", "bit_length":
		arity = 1
	case "overlay":
		arity = 3
		if len(operands) == 4 {
			arity = 4
		}
	case "substring", "substr":
		if len(operands) == 3 {
			arity = 3
		}
	}
	if name == "length" && len(operands) == 2 {
		source, encoding := operands[0].descriptor(), operands[1].descriptor()
		if source == nil || source.BaseType().ID() == sqltype.Bytea {
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
	text := len(operands) == arity
	for i, o := range operands {
		if binaryArgumentID(name, i) == sqltype.Int4 {
			d := o.descriptor()
			text = text && (d == nil || d.BaseType().ID() == sqltype.Int2 || d.BaseType().ID() == sqltype.Int4)
		} else {
			text = text && binaryTextType(o.descriptor())
		}
	}
	if (name == "substring" || name == "substr") && len(operands) >= 2 && len(operands) <= 3 && binaryTextType(operands[0].descriptor()) {
		regex := name == "substring"
		for _, o := range operands[1:] {
			regex = regex && binaryTextType(o.descriptor())
		}
		text = text || regex
	}
	if text {
		return nil, expressionError("0A000", "text overloads of "+name+" are unsupported", raw.Pos)
	}
	if len(operands) != arity {
		return missing()
	}
	known := false
	for i, o := range operands {
		if d := o.descriptor(); d != nil {
			target := binaryArgumentID(name, i)
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(target).Usage(), sqltype.Implicit); err != nil {
				return missing()
			}
			known = true
		}
	}
	if !known {
		return missing()
	}
	return b.finishBinary(name, raw.Pos, operands, raw.Syntax)
}

func (b *expressionBinder) finishBinary(name string, pos parser.Position, operands []comparisonOperand, syntax parser.FunctionSyntax) (BoundExpr, error) {
	args := make([]BoundExpr, len(operands))
	var usages [4]sqltype.Usage
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		target := b.typ(binaryArgumentID(name, i))
		arg, err := b.finalizeOperand(o, target)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target.Usage() {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			arg = coercionExpression{b.info(target.Usage(), arg.SourcePosition(), arg.Properties()), arg, plan, sqltype.Implicit}
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
	switch name {
	case "length", "octet_length", "bit_length", "position":
		id = sqltype.Int4
	case "crc32", "crc32c":
		id = sqltype.Int8
	case "md5":
		id = sqltype.Text
	}
	usage := b.typ(id).Usage()
	plan, err := sqltype.BindBinary(name, usage, usages[:len(operands)]...)
	if err != nil {
		return nil, err
	}
	node := binaryExpression{expressionInfo: b.info(usage, pos, properties), plan: plan, args: args, syntax: syntax}
	if err := node.validateSyntax(); err != nil {
		return nil, err
	}
	return node, nil
}

func binaryArgumentID(name string, index int) sqltype.ID {
	if ((name == "substring" || name == "substr") && index >= 1) || (name == "overlay" && index >= 2) {
		return sqltype.Int4
	}
	return sqltype.Bytea
}
