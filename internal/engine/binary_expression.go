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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.defaultConsumer && (name != "substring" || len(raw.Args) != 3) {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	conversion := name == "convert" || name == "convert_from" || name == "convert_to" || name == "encode" || name == "decode"
	var operands []comparisonOperand
	var names []string
	if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.stringOperand(r, conversion && binaryArgumentID(name, i) != sqltype.Bytea)
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
	if b.defaultConsumer {
		selected := true
		for _, operand := range operands {
			selected = selected && binaryTextType(operand.descriptor())
		}
		if selected {
			if !visible {
				return missing()
			}
			return b.bindSimilarSubstring(raw, operands)
		}
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	switch name {
	case "bit_count", "get_bit", "get_byte", "set_bit", "set_byte":
		return b.bindBinaryAccess(raw, visible, operands, names)
	}
	if conversion {
		arity := 2
		if name == "convert" {
			arity = 3
		}
		if !visible || len(operands) != arity {
			return missing()
		}
		for i, o := range operands {
			if o.descriptor() != nil {
				if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(binaryArgumentID(name, i)).Usage(), sqltype.Implicit); err != nil {
					return missing()
				}
			}
		}
		return b.finishBinary(name, raw.Pos, operands, raw.Syntax)
	}
	hash := false
	switch name {
	case "crc32", "crc32c", "sha224", "sha256", "sha384", "sha512":
		hash = true
	}
	if hash {
		if !visible || len(operands) != 1 {
			return missing()
		}
		d := operands[0].descriptor()
		// CRC/SHA have unique BYTEA signatures; shared MD5 selection is
		// owned by the string binder before reaching the binary provider.
		if d != nil && d.BaseType().ID() != sqltype.Bytea {
			return missing()
		}
		return b.finishBinary(name, raw.Pos, operands, raw.Syntax)
	}
	// PostgreSQL accepts VARIADIC spelling for these fixed scalar signatures;
	// it does not introduce an array argument or a variadic execution plan.
	selectedSlice := name == "substring" || name == "substr"
	if !visible || (raw.Variadic && !selectedSlice) {
		return missing()
	}
	arity := 2
	switch name {
	case "substring", "substr":
		if len(operands) == 3 {
			arity = 3
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
		if regex && len(operands) == 3 {
			return b.bindSimilarSubstring(raw, operands)
		}
		if !regex && text {
			// A known integer count selects the existing positional signature.
			// Preserve its input-conversion diagnostics before reporting the
			// native positional-TEXT capability boundary.
			for _, operand := range operands[1:] {
				if _, err := b.likeOperand(operand, b.typ(sqltype.Int4)); err != nil {
					return nil, err
				}
			}
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
		arg, err := b.likeOperand(o, target)
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
	switch name {
	case "length", "octet_length", "bit_length", "position", "get_bit", "get_byte":
		id = sqltype.Int4
	case "crc32", "crc32c", "bit_count":
		id = sqltype.Int8
	case "md5", "encode", "convert_from":
		id = sqltype.Text
	}
	usage := b.typ(id).Usage()
	info := b.info(usage, pos, properties)
	if name == "convert" || name == "convert_from" || name == "convert_to" {
		info.properties.Volatility = max(info.properties.Volatility, Stable)
	}
	inputInfo, err := collatedInfo(b.info(b.typ(sqltype.Text).Usage(), pos, properties), args...)
	if err != nil {
		return nil, err
	}
	if id == sqltype.Text {
		inputInfo.properties = info.properties
		info = inputInfo
	}
	plan, err := sqltype.BindBinary(name, info.usage, usages[:len(operands)]...)
	if err != nil {
		return nil, err
	}
	node := binaryExpression{expressionInfo: info, plan: plan, args: args, syntax: syntax}
	if err := node.validateSyntax(); err != nil {
		return nil, err
	}
	return node, nil
}

func binaryArgumentID(name string, index int) sqltype.ID {
	switch name {
	case "get_bit", "set_bit":
		if index == 1 {
			return sqltype.Int8
		}
		if index == 2 {
			return sqltype.Int4
		}
	case "get_byte", "set_byte":
		if index > 0 {
			return sqltype.Int4
		}
	case "convert", "convert_from":
		if index > 0 {
			return sqltype.Name
		}
	case "convert_to":
		if index == 0 {
			return sqltype.Text
		}
		return sqltype.Name
	case "encode":
		if index > 0 {
			return sqltype.Text
		}
	case "decode":
		return sqltype.Text
	}
	if ((name == "substring" || name == "substr") && index >= 1) || (name == "overlay" && index >= 2) {
		return sqltype.Int4
	}
	return sqltype.Bytea
}

// Only the released BYTEA and BIT signatures compete here. BIT participates
// in analysis, never execution; unknown input is converted after selection.
func (b *expressionBinder) bindBinaryAccess(raw parser.FunctionExpr, visible bool, operands []comparisonOperand, names []string) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	arity := 2
	if name == "bit_count" {
		arity = 1
	} else if name == "set_bit" || name == "set_byte" {
		arity = 3
	}
	if !visible || len(operands) != arity {
		return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	candidates := [2]bool{true, name == "bit_count" || name == "get_bit" || name == "set_bit"}
	exact := [2]int{}
	for candidate := range candidates {
		if !candidates[candidate] {
			continue
		}
		for i, operand := range operands {
			d := operand.descriptor()
			if d == nil {
				continue
			}
			target := binaryArgumentID(name, i)
			if candidate == 1 {
				if i == 0 {
					target = sqltype.Bit
				} else {
					target = sqltype.Int4
				}
			}
			if d.BaseType().ID() == target {
				exact[candidate]++
			}
			if _, err := sqltype.PlanCoercion(operand.node.ResultUsage(), b.typ(target).Usage(), sqltype.Implicit); err != nil {
				candidates[candidate] = false
				break
			}
		}
	}
	if candidates[0] && candidates[1] {
		if exact[0] > exact[1] {
			candidates[1] = false
		} else if exact[1] > exact[0] {
			candidates[0] = false
		} else {
			// The only differing unknown roles are BYTEA(U)/BIT(V)
			// and INT8/INT4 (neither preferred). No category winner exists.
			return nil, functionResolutionErrorForTypes(raw.Name, raw.Pos, names, true)
		}
	}
	if candidates[0] {
		return b.finishBinary(name, raw.Pos, operands, raw.Syntax)
	}
	if candidates[1] {
		return nil, expressionError("0A000", "bit overloads of "+name+" are unsupported", raw.Pos)
	}
	return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
}
