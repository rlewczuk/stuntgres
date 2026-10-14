package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type regexpCall struct {
	expressionInfo
	plan         sqltype.RegexpPlan
	args         []BoundExpr
	writtenSlots [7]uint8
	labels       [7]string
}
type regexpExpression struct{ regexpCall }

func (e regexpExpression) Eval(c *EvalContext) (sqltype.Value, error) { return e.evaluateScalar(c) }
func (call regexpCall) evaluateArguments(c *EvalContext) ([7]sqltype.Value, error) {
	var values [7]sqltype.Value
	if err := evaluationContext(c); err != nil {
		return values, err
	}
	for _, slot := range call.writtenSlots[:len(call.args)] {
		v, err := call.args[slot].Eval(c)
		if err != nil {
			pgRegexExecution(c).Reset()
			return values, err
		}
		values[slot] = v
	}
	return values, nil
}
func (call regexpCall) evaluateScalar(c *EvalContext) (sqltype.Value, error) {
	values, err := call.evaluateArguments(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return call.plan.Evaluate(c.Context, values, pgRegexExecution(c))
}
func (call regexpCall) evaluateSet(c *EvalContext) ([]sqltype.Value, error) {
	values, err := call.evaluateArguments(c)
	if err != nil {
		return nil, err
	}
	return call.plan.EvaluateSet(c.Context, values, pgRegexExecution(c))
}
func regexpSignature(name string, n int, replaceFlags bool) (labels [7]string, types [7]sqltype.ID, result sqltype.ID, valid bool) {
	labels = [7]string{"string", "pattern"}
	types = [7]sqltype.ID{sqltype.Text, sqltype.Text}
	result = sqltype.Text
	valid = n >= 2 && n <= 7
	switch name {
	case "regexp_count":
		valid = valid && n <= 4
		labels[2], labels[3] = "start", "flags"
		types[2], types[3] = sqltype.Int4, sqltype.Text
		result = sqltype.Int4
	case "regexp_instr":
		labels = [7]string{"string", "pattern", "start", "N", "endoption", "flags", "subexpr"}
		types = [7]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Text, sqltype.Int4}
		result = sqltype.Int4
	case "regexp_like", "regexp_match", "regexp_matches", "regexp_split_to_array", "regexp_split_to_table":
		valid = valid && n <= 3
		labels[2] = "flags"
		types[2] = sqltype.Text
		if name == "regexp_like" {
			result = sqltype.Bool
		}
		if name == "regexp_match" || name == "regexp_matches" || name == "regexp_split_to_array" {
			result = 1009
		}
	case "regexp_replace":
		valid = valid && n >= 3 && n <= 6
		labels = [7]string{"string", "pattern", "replacement", "start", "N", "flags"}
		types = [7]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Int4, sqltype.Text}
		if n == 4 && replaceFlags {
			labels[3] = "flags"
			types[3] = sqltype.Text
		}
	default:
		valid = false
	}
	return
}
func (b *expressionBinder) bindRegexpCall(raw parser.FunctionExpr, visible bool) (regexpCall, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var call regexpCall
	operands := make([]comparisonOperand, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.stringOperand(arg, true)
		if err != nil {
			return call, err
		}
		operands[i] = o
	}
	signature := func() error {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(o))
		}
		return functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if len(raw.Args) > 100 {
		return call, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return call, fmt.Errorf("invalid regexp argument labels")
	}
	named := false
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			if named {
				return call, expressionError("42601", "positional argument cannot follow named argument", raw.Args[i].SourcePosition())
			}
			continue
		}
		named = true
		for _, previous := range raw.ArgNames[:i] {
			if previous.Text == label.Text {
				return call, expressionError("42601", fmt.Sprintf("argument name %q used more than once", label.Text), label.Pos)
			}
		}
	}
	replaceFlags := true
	if name == "regexp_replace" && len(operands) == 4 {
		for i, o := range operands {
			if len(raw.ArgNames) > i && raw.ArgNames[i].Text == "start" {
				replaceFlags = false
				break
			}
			if i == 3 && (len(raw.ArgNames) == 0 || raw.ArgNames[i].Text == "") && o.descriptor() != nil {
				_, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(sqltype.Int4).Usage(), sqltype.Implicit)
				replaceFlags = err != nil
			}
		}
	}
	if len(operands) > 100 {
		return call, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	labels, types, result, valid := regexpSignature(name, len(operands), replaceFlags)
	if !visible || raw.Variadic || !valid {
		return call, signature()
	}
	var supplied [7]bool
	for i, o := range operands {
		slot := i
		if len(raw.ArgNames) > 0 && raw.ArgNames[i].Text != "" {
			slot = -1
			for j, label := range labels[:len(operands)] {
				if label == raw.ArgNames[i].Text {
					slot = j
					break
				}
			}
		}
		if slot < 0 || supplied[slot] {
			return call, signature()
		}
		supplied[slot] = true
		call.writtenSlots[i] = uint8(slot)
		if len(raw.ArgNames) > 0 {
			call.labels[i] = raw.ArgNames[i].Text
		}
		if o.descriptor() != nil {
			_, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(types[slot]).Usage(), sqltype.Implicit)
			if types[slot] == sqltype.Text && o.node.ResultUsage().Type.BaseType().ID() == sqltype.Char {
				_, err = sqltype.PlanExtremaCharText(o.node.ResultUsage(), b.typ(sqltype.Text).Usage())
			}
			if err != nil {
				return call, signature()
			}
		}
	}
	call.args = make([]BoundExpr, len(operands))
	usages := make([]sqltype.Usage, len(operands))
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		slot := call.writtenSlots[i]
		target := b.typ(types[slot])
		arg, err := b.finalizeOperand(o, target)
		if err != nil {
			return call, err
		}
		targetUsage := target.Usage()
		if target.ID() == sqltype.Text && arg.ResultUsage().Collation.ID != 0 {
			targetUsage.Collation = arg.ResultUsage().Collation
		}
		if arg.ResultUsage() != targetUsage {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), targetUsage, sqltype.Implicit)
			if target.ID() == sqltype.Text && arg.ResultUsage().Type.BaseType().ID() == sqltype.Char {
				plan, err = sqltype.PlanExtremaCharText(arg.ResultUsage(), targetUsage)
			}
			if err != nil {
				return call, err
			}
			info := b.info(targetUsage, arg.SourcePosition(), coercionProperties(arg, plan))
			if target.ID() == sqltype.Text {
				info, err = b.collatedInfo(info, arg)
				if err != nil {
					return call, err
				}
			}
			arg = coercionExpression{info, arg, plan, sqltype.Implicit}
		}
		call.args[slot] = arg
		usages[slot] = arg.ResultUsage()
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	if sqltype.RegexpFunctionKind(name) == sqltype.RegexpMatches || sqltype.RegexpFunctionKind(name) == sqltype.RegexpSplitTable {
		properties.StageDependent = true
	}
	input, err := b.collatedInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, properties), call.args...)
	if err != nil {
		return call, err
	}
	info := b.info(b.typ(result).Usage(), raw.Pos, properties)
	if info.usage.Collation.ID != 0 {
		info.usage.Collation = input.usage.Collation
		info.collationConflict = input.collationConflict
	}
	call.expressionInfo = info
	call.plan, err = sqltype.BindRegexpFunction(name, usages, info.usage, input.usage.Collation, input.collationConflict)
	if err == nil && call.plan.ReturnsSet() {
		err = b.regexpPlacement(call)
	}
	return call, err
}
func (call regexpCall) sql(name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	text := name + "("
	for i, slot := range call.writtenSlots[:len(call.args)] {
		if i > 0 {
			text += ", "
		}
		if call.labels[i] != "" {
			text += identifier(call.labels[i]) + " => "
		}
		s, err := render(call.args[slot])
		if err != nil {
			return "", err
		}
		text += s
	}
	return text + ")", nil
}

func (call regexpCall) mapChildren(view CatalogRead, info expressionInfo, child func(BoundExpr) BoundExpr) (regexpCall, error) {
	call.args = append([]BoundExpr(nil), call.args...)
	usages := make([]sqltype.Usage, len(call.args))
	for i, old := range call.args {
		call.args[i] = child(old)
		usages[i] = call.args[i].ResultUsage()
	}
	input, err := collatedInfo(view, expressionInfo{usage: call.args[0].ResultUsage(), pos: info.pos}, call.args...)
	if err != nil {
		return call, err
	}
	call.expressionInfo = info
	call.plan, err = sqltype.BindRegexpFunction(call.plan.Name(), usages, info.usage, input.usage.Collation, input.collationConflict)
	return call, err
}
func preflightRegexpCall(c *EvalContext, call regexpCall, decisive bool) (regexpCall, bool, error) {
	var args []BoundExpr
	for i, old := range call.args {
		next, did, err := preflightCoercionsWithPolicy(c, old, decisive)
		if err != nil {
			return call, false, err
		}
		if did {
			if args == nil {
				args = append([]BoundExpr(nil), call.args...)
			}
			args[i] = next
		}
	}
	if args != nil {
		call.args = args
	}
	return call, args != nil, nil
}
func sameRegexpCall(a, b regexpCall, same func(BoundExpr, BoundExpr) bool) bool {
	if a.plan != b.plan || len(a.args) != len(b.args) {
		return false
	}
	for i, arg := range a.args {
		if !same(arg, b.args[i]) {
			return false
		}
	}
	return true
}

func (b *expressionBinder) regexpPlacement(call regexpCall) error {
	context := b.regexpClause
	hint := context == "CASE" || context == "COALESCE"
	if context == "" && b.argumentOwner.active {
		message := "aggregate function calls cannot contain set-returning function calls"
		if b.argumentOwner.window {
			message = "window function calls cannot contain set-returning function calls"
		}
		err := expressionError("0A000", message, call.pos).(*sqlstate.Error)
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
	return rejectRegexpSets(regexpSetExpression{call}, context, hint)
}
