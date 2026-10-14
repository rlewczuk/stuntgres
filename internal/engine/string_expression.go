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
	textual [4]bool
	syntax  parser.StringSyntax
}

func (e stringExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.plan.Name() == "string_to_table" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: unlowered string_to_table expression")
	}
	if e.plan.Strict() {
		for _, arg := range e.args {
			if randomLiteralNull(arg) {
				return sqltype.Null(e.usage.Type), nil
			}
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
	output := c.Output
	output.Regclass = c.liveScope()
	output.Regtype = c.liveScope()
	output.Regrole = c.liveScope()
	output.Regcollation, output.Regnamespace = c.liveScope(), c.liveScope()
	output.Regproc = c.liveScope()
	output.Operator = c.liveScope()
	output.RegTS = c.liveScope()
	return e.plan.Evaluate(c.Context, values, c.Input, output)
}
func (b *expressionBinder) stringNode(name string, result sqltype.Usage, pos parser.Position, args []BoundExpr, textual [4]bool, syntax parser.StringSyntax) (BoundExpr, error) {
	var usages [4]sqltype.Usage
	p := ExprProperties{Volatility: Immutable}
	if name == "string_to_table" {
		p.StageDependent = true
	}
	if name == "||" && (!textual[0] || !textual[1]) {
		p.Volatility = Stable
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
		if name == "||" && !textual[i] && usages[i].Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
		child := arg.Properties()
		p.Volatility = max(p.Volatility, child.Volatility)
		p.RowDependent = p.RowDependent || child.RowDependent
		p.ParameterDependent = p.ParameterDependent || child.ParameterDependent
		p.StageDependent = p.StageDependent || child.StageDependent
		p.DomainDependent = p.DomainDependent || child.DomainDependent
		p.RuntimeDomainConversion = p.RuntimeDomainConversion || child.RuntimeDomainConversion
	}
	info := b.info(result, pos, p)
	inputInfo := info
	if name == "position" || name == "strpos" || name == "starts_with" {
		// Search/prefix demand input collation despite a noncollatable result.
		inputInfo = b.info(b.typ(sqltype.Text).Usage(), pos, p)
	}
	inputInfo, err := b.collatedInfo(inputInfo, args...)
	if err != nil {
		return nil, err
	}
	if result.Collation.ID != 0 {
		info = inputInfo
	}
	plan, err := sqltype.BindString(name, info.usage, b.typ(sqltype.Text).Usage(), usages[:len(args)], textual, inputInfo.collationConflict)
	if err != nil {
		return nil, err
	}
	return stringExpression{info, plan, args, textual, syntax}, nil
}

// COLLATE resolves before overload finalization. Contextualize only textual
// roles, preserving each established parameter's input descriptor.
func (b *expressionBinder) stringOperand(raw parser.Expr, textual bool) (comparisonOperand, error) {
	policy := expressionPolicy{context: projectionContext, stringInput: textual}
	if _, collated := raw.(parser.CollateExpr); collated && textual {
		policy.expected = b.typ(sqltype.Text).Usage()
		child := raw
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
	return b.comparisonOperandPolicy(raw, policy)
}

func (b *expressionBinder) bindStringFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [4]comparisonOperand
	var operands []comparisonOperand
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.stringOperand(r, !sqltype.StringIntegerArgument(name, i))
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
	if sqltype.StringBaseConversion(name) {
		return b.finishStringNumericBase(raw, visible, operands)
	}
	count := name == "length" || name == "char_length" || name == "character_length" || name == "octet_length" || name == "bit_length"
	search := name == "position" || name == "strpos"
	replacement := name == "replace" || name == "translate" || name == "starts_with" || name == "overlay"
	// The real bytea/name overload remains an explicit capability boundary.
	if visible && name == "length" && len(operands) == 2 {
		source, encoding := operands[0].descriptor(), operands[1].descriptor()
		if source == nil || source.BaseType().ID() == sqltype.Bytea {
			var conversionErr error
			if encoding != nil {
				_, conversionErr = sqltype.PlanCoercion(operands[1].node.ResultUsage(), b.typ(sqltype.Name).Usage(), sqltype.Implicit)
			}
			if conversionErr == nil {
				if b.defaultConsumer {
					return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
				}
				return nil, expressionError("0A000", "encoding-aware length is unsupported", raw.Pos)
			}
		}
	}
	// Shared names select from once-analyzed textual roles. Overlay's INT4
	// positions do not participate in TEXT/BYTEA candidate selection.
	binary := visible && ((!raw.Variadic && len(operands) == 2 && (name == "btrim" || name == "ltrim" || name == "rtrim")) ||
		(!raw.Variadic && len(operands) == 1 && (name == "length" || name == "octet_length" || name == "bit_length")) ||
		(len(operands) == 2 && name == "position") || ((name == "reverse" || name == "md5") && len(operands) == 1) || (name == "overlay" && (len(operands) == 3 || len(operands) == 4)))
	knownBinary := false
	for i, o := range operands {
		if sqltype.StringIntegerArgument(name, i) {
			continue
		}
		if d := o.descriptor(); d != nil {
			binary = binary && d.BaseType().ID() == sqltype.Bytea
			knownBinary = knownBinary || d.BaseType().ID() == sqltype.Bytea
		}
	}
	if binary && knownBinary {
		valid := true
		for i, o := range operands {
			if d := o.descriptor(); d != nil {
				_, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(binaryArgumentID(name, i)).Usage(), sqltype.Implicit)
				valid = valid && err == nil
			}
		}
		if valid {
			if b.defaultConsumer {
				return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
			}
			syntax := raw.Syntax
			if raw.StringSyntax == parser.TrimStringSyntax {
				syntax = parser.SQLFunctionSyntax
			}
			return b.finishBinary(name, raw.Pos, operands, syntax)
		}
	}
	// UNKNOWN prefers the string-category NAME overload. Known operands choose
	// only an existing implicit INT4 or NAME edge; retain their source typing.
	encodingID := sqltype.Name
	if name == "to_ascii" && len(operands) > 1 && operands[1].descriptor() != nil {
		if _, err := sqltype.PlanCoercion(operands[1].node.ResultUsage(), b.typ(sqltype.Int4).Usage(), sqltype.Implicit); err == nil {
			encodingID = sqltype.Int4
		}
	}
	split := name == "split_part" || name == "string_to_array" || name == "string_to_table"
	valid := visible && (!raw.Variadic || split || count || search || replacement || name == "lpad" || name == "rpad" || name == "repeat" || name == "reverse" || name == "md5")
	switch {
	case name == "split_part":
		valid = valid && len(operands) == 3
	case name == "string_to_array" || name == "string_to_table":
		valid = valid && (len(operands) == 2 || len(operands) == 3)
	case name == "ascii" || name == "chr" || name == "unistr" || name == "unicode_assigned":
		valid = valid && len(operands) == 1
	case name == "lpad" || name == "rpad":
		valid = valid && (len(operands) == 2 || len(operands) == 3)
	case name == "repeat":
		valid = valid && len(operands) == 2
	case name == "reverse" || name == "md5":
		valid = valid && len(operands) == 1
	case name == "overlay":
		valid = valid && (len(operands) == 3 || len(operands) == 4)
	case name == "replace" || name == "translate":
		valid = valid && len(operands) == 3
	case search || name == "starts_with":
		valid = valid && len(operands) == 2
	case count || name == "lower" || name == "upper" || name == "casefold" || name == "initcap":
		valid = valid && len(operands) == 1
	default:
		valid = valid && len(operands) >= 1 && len(operands) <= 2
	}
	for i, o := range operands {
		if d := o.descriptor(); d != nil {
			if name == "to_ascii" && i == 1 {
				_, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(encodingID).Usage(), sqltype.Implicit)
				valid = valid && err == nil
				continue
			}
			if sqltype.StringIntegerArgument(name, i) {
				_, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(sqltype.Int4).Usage(), sqltype.Implicit)
				valid = valid && err == nil
			} else {
				valid = valid && sqltype.StringTextInput(d.BaseType().ID())
			}
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
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	args := make([]BoundExpr, len(operands))
	var textual [4]bool
	for i, o := range operands {
		target := b.typ(sqltype.Text)
		textual[i] = true
		if sqltype.StringIntegerArgument(name, i) {
			target = b.typ(sqltype.Int4)
			textual[i] = false
		}
		if name == "to_ascii" && i == 1 {
			target = b.typ(encodingID)
			textual[i] = false
		}
		var err error
		args[i], err = b.finalizeOperand(o, target)
		if err != nil {
			return nil, err
		}
		if !textual[i] && args[i].ResultUsage() != target.Usage() {
			plan, err := sqltype.PlanCoercion(args[i].ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			args[i] = coercionExpression{b.info(target.Usage(), args[i].SourcePosition(), args[i].Properties()), args[i], plan, sqltype.Implicit}
		}
	}
	result := b.typ(sqltype.Text).Usage()
	if name == "string_to_array" {
		result = b.typ(b.typ(sqltype.Text).AssociatedArray()).Usage()
	}
	if name == "is_normalized" || name == "starts_with" || name == "unicode_assigned" {
		result = b.typ(sqltype.Bool).Usage()
	}
	if count || search || name == "ascii" {
		result = b.typ(sqltype.Int4).Usage()
	}
	if name == "position" && raw.Syntax == parser.SQLFunctionSyntax {
		raw.StringSyntax = parser.PositionStringSyntax
	}
	if name == "overlay" && raw.Syntax == parser.SQLFunctionSyntax {
		raw.StringSyntax = parser.OverlayStringSyntax
	}
	return b.stringNode(name, result, raw.Pos, args, textual, raw.StringSyntax)
}

func (b *expressionBinder) bindStringPrefix(raw parser.OperatorExpr) (BoundExpr, error) {
	var operands [2]comparisonOperand
	var types [2]*sqltype.Descriptor
	for i, child := range raw.Args {
		o, err := b.stringOperand(child, true)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i], types[i] = o, o.descriptor()
		}
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	valid := visible && len(raw.Args) == 2
	for _, d := range types {
		valid = valid && (d == nil || sqltype.StringTextInput(d.BaseType().ID()))
	}
	if !valid {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), types[0], types[1], raw)
	}
	args := make([]BoundExpr, 2)
	for i, o := range operands {
		args[i], err = b.finalizeOperand(o, b.typ(sqltype.Text))
		if err != nil {
			return nil, err
		}
	}
	return b.stringNode("starts_with", b.typ(sqltype.Bool).Usage(), raw.Pos, args, [4]bool{true, true}, parser.PrefixStringSyntax)
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
	if visible && ((types[0] != nil && types[0].BaseType().ID() == sqltype.Bytea && (types[1] == nil || types[1].BaseType().ID() == sqltype.Bytea)) ||
		(types[1] != nil && types[1].BaseType().ID() == sqltype.Bytea && types[0] == nil)) {
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
				id := t.BaseType().ID()
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
				} else if !sqltype.StringScalar(t) {
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
	return b.stringNode("||", b.typ(sqltype.Text).Usage(), raw.Pos, args, [4]bool{textual[0], textual[1]}, parser.OrdinaryStringSyntax)
}

// Both PostgreSQL presentation and native replay preserve special syntax, but
// each renderer owns its child formatting and literal transport.
func (n stringExpression) specialSQL(parts []string) (string, error) {
	source, set := parts[0], ""
	if len(parts) > 1 {
		set = parts[1]
	}
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
	case parser.PositionStringSyntax:
		if n.plan.Name() != "position" || len(n.args) != 2 {
			return "", fmt.Errorf("stuntgres: invalid POSITION syntax provider")
		}
		return renderBinaryCall(n.plan.Name(), parser.SQLFunctionSyntax, parts, n.plan.Name(), func(s string) (string, error) { return s, nil })
	case parser.OverlayStringSyntax:
		return renderBinaryCall(n.plan.Name(), parser.SQLFunctionSyntax, parts, n.plan.Name(), func(s string) (string, error) { return s, nil })
	case parser.PrefixStringSyntax:
		if n.plan.Name() != "starts_with" || len(parts) != 2 {
			return "", fmt.Errorf("stuntgres: invalid prefix syntax provider")
		}
		return "(" + source + " OPERATOR(pg_catalog.^@) " + set + ")", nil
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
