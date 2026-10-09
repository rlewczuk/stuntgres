package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type stringExpression struct {
	expressionInfo
	plan    sqltype.StringPlan
	args    []BoundExpr
	textual [2]bool
	syntax  parser.StringSyntax
}

func (e stringExpression) Eval(c *EvalContext) (sqltype.Value, error) {
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
	output := c.Output
	output.Regclass = c.statementScope
	return e.plan.Evaluate(c.Context, values, c.Input, output)
}
func (b *expressionBinder) stringNode(name string, result sqltype.Usage, pos parser.Position, args []BoundExpr, textual [2]bool, syntax parser.StringSyntax) (BoundExpr, error) {
	var usages [2]sqltype.Usage
	p := ExprProperties{Volatility: Immutable}
	if name == "||" && (!textual[0] || !textual[1]) {
		p.Volatility = Stable
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
		if name == "||" && !textual[i] && usages[i].Type.ID() == sqltype.Regclass {
			b.regclassConcat = true
		}
		child := arg.Properties()
		p.Volatility = max(p.Volatility, child.Volatility)
		p.RowDependent = p.RowDependent || child.RowDependent
		p.ParameterDependent = p.ParameterDependent || child.ParameterDependent
		p.StageDependent = p.StageDependent || child.StageDependent
	}
	info, err := collatedInfo(b.info(result, pos, p), args...)
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.BindString(name, info.usage, b.typ(sqltype.Text).Usage(), usages[:len(args)], textual, info.collationConflict)
	if err != nil {
		return nil, err
	}
	return stringExpression{info, plan, args, textual, syntax}, nil
}
func (b *expressionBinder) bindStringFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]comparisonOperand
	var operands []comparisonOperand
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, r := range raw.Args {
		policy := expressionPolicy{context: projectionContext}
		if _, collated := r.(parser.CollateExpr); collated {
			// COLLATE resolves before overload finalization. Give an unknown
			// input the string candidate's TEXT context before that lookup;
			// already typed inputs retain their type and signature diagnostics.
			policy.expected = b.typ(sqltype.Text).Usage()
			child := r
			for {
				collation, ok := child.(parser.CollateExpr)
				if !ok {
					break
				}
				child = collation.Child
			}
			if parameter, ok := child.(parser.ParameterExpr); ok {
				if established := b.parameters[parameter.Number]; established.Type != nil {
					policy.expected = established
				}
			}
		}
		o, err := b.comparisonOperandPolicy(r, policy)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	// Shared trim spellings have TEXT(1/2) and BYTEA(2) candidates. Select
	// from these already-analyzed operands, never by retrying a raw binder.
	binary := visible && !raw.Variadic && len(operands) == 2 &&
		(name == "btrim" || name == "ltrim" || name == "rtrim")
	knownBinary := false
	for _, o := range operands {
		if d := o.descriptor(); d != nil {
			binary = binary && d.ID() == sqltype.Bytea
			knownBinary = knownBinary || d.ID() == sqltype.Bytea
		}
	}
	if binary && knownBinary {
		if b.defaultConsumer {
			return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
		}
		syntax := parser.OrdinaryFunctionSyntax
		if raw.StringSyntax == parser.TrimStringSyntax {
			syntax = parser.SQLFunctionSyntax
		}
		return b.finishBinary(name, raw.Pos, operands, syntax)
	}
	valid := visible && !raw.Variadic && len(operands) >= 1 && len(operands) <= 2
	if name == "lower" || name == "upper" || name == "casefold" || name == "initcap" {
		valid = valid && len(operands) == 1
	}
	for _, o := range operands {
		if d := o.descriptor(); d != nil && !sqltype.StringTextInput(d.ID()) {
			valid = false
		}
	}
	if !valid {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		return nil, functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	args := make([]BoundExpr, len(operands))
	for i, o := range operands {
		var err error
		args[i], err = b.finalizeOperand(o, b.typ(sqltype.Text))
		if err != nil {
			return nil, err
		}
	}
	result := b.typ(sqltype.Text).Usage()
	if name == "is_normalized" {
		result = b.typ(sqltype.Bool).Usage()
	}
	return b.stringNode(name, result, raw.Pos, args, [2]bool{true, true}, raw.StringSyntax)
}
func (b *expressionBinder) bindStringConcat(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 {
		return nil, expressionError("42883", "operator does not exist: ||", raw.Pos)
	}
	var operands [2]comparisonOperand
	var types [2]*sqltype.Descriptor
	for i, r := range raw.Args {
		o, err := b.comparisonOperand(r)
		if err != nil {
			return nil, err
		}
		operands[i] = o
		types[i] = o.descriptor()
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	// Exact BYTEA/BYTEA (including one unknown assumed from its known peer)
	// wins before the polymorphic text siblings. UNKNOWN/UNKNOWN prefers TEXT.
	if visible && ((types[0] != nil && types[0].ID() == sqltype.Bytea && (types[1] == nil || types[1].ID() == sqltype.Bytea)) ||
		(types[1] != nil && types[1].ID() == sqltype.Bytea && types[0] == nil)) {
		return b.finishBinary("||", raw.Pos, operands[:], parser.OrdinaryFunctionSyntax)
	}
	// A=TEXT/TEXT, B=TEXT/ANYNONARRAY, C=ANYNONARRAY/TEXT. A
	// polymorphic slot is viable but never an exact or preferred TEXT match.
	signatures := [3][2]bool{{true, true}, {true, false}, {false, true}}
	var candidates [3]int
	count := 0
	bestExact, bestPreferred := -1, -1
	if visible {
		for k, s := range signatures {
			viable := true
			exact, preferred := 0, 0
			for i, t := range types {
				if t == nil {
					continue
				}
				id := t.ID()
				if s[i] {
					if !sqltype.StringTextInput(id) {
						viable = false
					}
					if id == sqltype.Text {
						exact++
					}
					if sqltype.StringCategory(id) {
						preferred++
					}
				} else if !sqltype.StringScalar(id) {
					viable = false
				}
			}
			if !viable {
				continue
			}
			if exact > bestExact || exact == bestExact && preferred > bestPreferred {
				count = 0
				bestExact = exact
				bestPreferred = preferred
			}
			if exact == bestExact && preferred == bestPreferred {
				candidates[count] = k
				count++
			}
		}
	}
	for i, t := range types {
		if t != nil || count < 2 {
			continue
		}
		preferText := false
		for _, k := range candidates[:count] {
			preferText = preferText || signatures[k][i]
		}
		if preferText {
			n := 0
			for _, k := range candidates[:count] {
				if signatures[k][i] {
					candidates[n] = k
					n++
				}
			}
			count = n
		}
	}
	if count != 1 {
		if count == 0 {
			return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), types[0], types[1], raw)
		}
		typeName := func(t *sqltype.Descriptor) string {
			if t == nil {
				return "unknown"
			}
			return t.DiagnosticName()
		}
		d := expressionError("42725", fmt.Sprintf("operator is not unique: %s %s %s", typeName(types[0]), objectNameText(raw.Name), typeName(types[1])), raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
		return nil, d
	}
	textual := signatures[candidates[0]]
	args := make([]BoundExpr, 2)
	for i, o := range operands {
		if o.descriptor() == nil {
			args[i], err = b.finalizeOperand(o, b.typ(sqltype.Text))
			if err != nil {
				return nil, err
			}
		} else {
			args[i] = o.node
		}
	}
	return b.stringNode("||", b.typ(sqltype.Text).Usage(), raw.Pos, args, textual, parser.OrdinaryStringSyntax)
}

// Both PostgreSQL presentation and native replay preserve special syntax, but
// each renderer owns its child formatting and literal transport.
func (n stringExpression) specialSQL(source, set string) (string, error) {
	form := ""
	if n.syntax == parser.NormalizeStringSyntax || n.syntax == parser.NormalizedStringSyntax {
		if len(n.args) == 2 {
			literal, ok := n.args[1].(literalExpression)
			if !ok {
				return "", fmt.Errorf("stuntgres: invalid normalization syntax form")
			}
			var err error
			form, err = literal.value.Text()
			if err != nil {
				return "", err
			}
		}
	}
	switch n.syntax {
	case parser.TrimStringSyntax:
		return trimSyntaxSQL(n.plan.Name(), source, set, len(n.args))
	case parser.NormalizeStringSyntax:
		if form != "" {
			source += ", " + form
		}
		return "NORMALIZE(" + source + ")", nil
	case parser.NormalizedStringSyntax:
		if form != "" {
			form += " "
		}
		return "(" + source + " IS " + form + "NORMALIZED)", nil
	default:
		return "", fmt.Errorf("stuntgres: invalid special string syntax")
	}
}

func trimSyntaxSQL(name, source, set string, count int) (string, error) {
	direction := ""
	switch name {
	case "btrim":
		direction = "BOTH"
	case "ltrim":
		direction = "LEADING"
	case "rtrim":
		direction = "TRAILING"
	default:
		return "", fmt.Errorf("stuntgres: invalid trim syntax provider")
	}
	if count == 2 {
		direction += " " + set
	} else if count != 1 {
		return "", fmt.Errorf("stuntgres: invalid trim syntax arity")
	}
	return "TRIM(" + direction + " FROM " + source + ")", nil
}
