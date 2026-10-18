package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRegexpCall(raw parser.FunctionExpr, visible bool) (functions.RegexpCall[BoundExpr, ExprContext, ExprProperties], error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var call functions.RegexpCall[BoundExpr, ExprContext, ExprProperties]
	operands := make([]functions.Argument[BoundExpr], len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.StringTextArgument})
		if err != nil {
			return call, err
		}
		operands[i] = o
	}
	signature := func() error {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(o.Usage.Type))
		}
		return functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if len(raw.Args) > 100 {
		return call, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return call, fmt.Errorf("invalid regexp argument labels")
	case issue.Positional:
		return call, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return call, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	replaceFlags, probe := functions.RegexpReplaceSelection(raw, operands)
	var integer *sqltype.Descriptor
	if probe != nil {
		integer = b.typ(sqltype.Int4)
		replaceFlags = functions.RegexpReplaceFlags(probe.Usage, integer.Usage())
	}
	labels, types, result, valid := functions.RegexpSignature(name, len(operands), replaceFlags)
	if !visible || raw.Variadic || !valid {
		return call, signature()
	}
	call.WrittenSlots, call.Labels, valid = functions.RegexpSlots(raw, labels, len(operands))
	if !valid {
		return call, signature()
	}
	var text *sqltype.Descriptor
	var targets [7]sqltype.Usage
	for i, operand := range operands {
		slot := call.WrittenSlots[i]
		var target *sqltype.Descriptor
		if types[slot] == sqltype.Text {
			if text == nil {
				text = b.typ(sqltype.Text)
			}
			target = text
		} else {
			if integer == nil {
				integer = b.typ(sqltype.Int4)
			}
			target = integer
		}
		targets[slot] = target.Usage()
		if !functions.RegexpCompatible(operand.Usage, targets[slot]) {
			return call, signature()
		}
	}
	var err error
	call.Args, err = functions.CoerceRegexpArguments(b, operands, call.WrittenSlots, targets)
	if err != nil {
		return call, err
	}
	properties := ExprProperties{Volatility: Immutable}
	for _, arg := range call.Args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	properties.StageDependent = properties.StageDependent || functions.RegexpStageDependent(name)
	input, err := b.collatedInfo(b.info(text.Usage(), raw.Pos, properties), call.Args...)
	if err != nil {
		return call, err
	}
	resultType := text
	if result != sqltype.Text {
		resultType = b.typ(result)
	}
	info := b.info(resultType.Usage(), raw.Pos, properties)
	if info.Usage.Collation.ID != 0 {
		info.Usage.Collation = input.Usage.Collation
		info.CollationConflict = input.CollationConflict
	}
	call, err = functions.NewRegexpCall[BoundExpr, ExprContext](functionInfo(info), name, call.Args, call.WrittenSlots, call.Labels, input.Usage.Collation, input.CollationConflict)
	if err == nil && call.Plan.ReturnsSet() {
		err = b.regexpPlacement(call)
	}
	if err == nil {
		info, err = b.builtinRoutineInfo(info, regexpRoutineProvider, name, types[:len(operands)]...)
		if err == nil {
			call.Info = functionInfo(info)
		}
	}
	return call, err
}
func regexpSQL(call functions.RegexpCall[BoundExpr, ExprContext, ExprProperties], name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	text := name + "("
	for i, slot := range call.WrittenSlots[:len(call.Args)] {
		if i > 0 {
			text += ", "
		}
		if call.Labels[i] != "" {
			text += identifier(call.Labels[i]) + " => "
		}
		s, err := render(call.Args[slot])
		if err != nil {
			return "", err
		}
		text += s
	}
	return text + ")", nil
}

func mapRegexpChildren(call functions.RegexpCall[BoundExpr, ExprContext, ExprProperties], view CatalogRead, info expressionInfo, child func(BoundExpr) BoundExpr) (functions.RegexpCall[BoundExpr, ExprContext, ExprProperties], error) {
	call.Args = append([]BoundExpr(nil), call.Args...)
	for i, old := range call.Args {
		call.Args[i] = child(old)
	}
	input, err := collatedInfo(view, expressionInfo{Usage: call.Args[0].ResultUsage(), Pos: info.Pos}, call.Args...)
	if err != nil {
		return call, err
	}
	return call.Reconstruct(functionInfo(info), call.Args, input.Usage.Collation, input.CollationConflict)
}
func preflightRegexpCall(c *EvalContext, call functions.RegexpCall[BoundExpr, ExprContext, ExprProperties], decisive bool) (functions.RegexpCall[BoundExpr, ExprContext, ExprProperties], bool, error) {
	var args []BoundExpr
	for i, old := range call.Args {
		next, did, err := preflightCoercionsWithPolicy(c, old, decisive)
		if err != nil {
			return call, false, err
		}
		if did {
			if args == nil {
				args = append([]BoundExpr(nil), call.Args...)
			}
			args[i] = next
		}
	}
	if args != nil {
		call.Args = args
	}
	return call, args != nil, nil
}
func sameRegexpCall(a, b functions.RegexpCall[BoundExpr, ExprContext, ExprProperties], same func(BoundExpr, BoundExpr) bool) bool {
	if a.Plan != b.Plan || len(a.Args) != len(b.Args) {
		return false
	}
	for i, arg := range a.Args {
		if !same(arg, b.Args[i]) {
			return false
		}
	}
	return true
}

func (b *expressionBinder) regexpPlacement(call functions.RegexpCall[BoundExpr, ExprContext, ExprProperties]) error {
	context := b.regexpClause
	hint := context == "CASE" || context == "COALESCE"
	if context == "" && b.argumentOwner.active {
		message := "aggregate function calls cannot contain set-returning function calls"
		if b.argumentOwner.window {
			message = "window function calls cannot contain set-returning function calls"
		}
		err := expressionError("0A000", message, call.SourcePosition()).(*sqlstate.Error)
		err.Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
		return err
	}
	if context == "" {
		switch {
		case b.executeParameters:
			context = "EXECUTE parameters"
		case b.generatedOwner != nil:
			context = "column generation expressions"
		case b.indexPredicateConsumer:
			context = "index predicates"
		case b.indexConsumer:
			context = "index expressions"
		case b.checkConsumer:
			context = "check constraints"
		case b.defaultConsumer:
			context = "DEFAULT expressions"
		case b.policyConsumer && b.queryFrame == 0:
			return sqlError("0A000", "set-returning functions are not allowed in policy expressions")
		default:
			switch b.aggregateClause {
			case "WHERE", "HAVING", "FILTER", "JOIN conditions", "LIMIT", "OFFSET", "UPDATE", "RETURNING", "VALUES", "window RANGE", "window ROWS", "window GROUPS":
				context = b.aggregateClause
				if context == "window RANGE" || context == "window ROWS" || context == "window GROUPS" {
					context = "window definitions"
				}
			}
		}
	}
	if context == "" {
		return nil
	}
	return rejectRegexpSets(functions.RegexpSet[BoundExpr, ExprContext, ExprProperties]{RegexpCall: call}, context, hint)
}
