package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) sameGroupExpression(a, other BoundExpr) bool {
	if a == nil || other == nil {
		return a == nil && other == nil
	}
	if a.ResultUsage() != other.ResultUsage() {
		return false
	}
	same := b.sameGroupExpression
	switch e := a.(type) {
	case literalExpression:
		o, ok := other.(literalExpression)
		if !ok {
			return false
		}
		return sqltype.SameDatum(e.value, o.value)
	case columnExpression:
		o, ok := other.(columnExpression)
		return ok && e.origin == o.origin && e.scope == o.scope && e.frame == o.frame && e.slot == o.slot
	case parameterExpression:
		o, ok := other.(parameterExpression)
		return ok && e.number == o.number
	case coercionExpression:
		o, ok := other.(coercionExpression)
		return ok && e.plan == o.plan && e.mode == o.mode && same(e.child, o.child)
	case arithmeticExpression:
		o, ok := other.(arithmeticExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case comparisonExpression:
		o, ok := other.(comparisonExpression)
		return ok && e.predicate == o.predicate && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case logicalExpression:
		o, ok := other.(logicalExpression)
		return ok && e.op == o.op && same(e.left, o.left) && same(e.right, o.right)
	case nullTestExpression:
		o, ok := other.(nullTestExpression)
		return ok && e.not == o.not && same(e.child, o.child)
	case truthTestExpression:
		o, ok := other.(truthTestExpression)
		return ok && e.truth == o.truth && e.not == o.not && same(e.child, o.child)
	case distinctExpression:
		o, ok := other.(distinctExpression)
		return ok && e.equality == o.equality && e.not == o.not && same(e.left, o.left) && same(e.right, o.right)
	case caseExpression:
		o, ok := other.(caseExpression)
		if !ok || len(e.arms) != len(o.arms) || !same(e.test, o.test) || !same(e.otherwise, o.otherwise) {
			return false
		}
		for i, arm := range e.arms {
			otherArm := o.arms[i]
			if arm.testPlan != otherArm.testPlan || arm.equality != otherArm.equality || arm.testUsage != otherArm.testUsage || !same(arm.condition, otherArm.condition) || !same(arm.result, otherArm.result) {
				return false
			}
		}
		return true
	case conditionalExpression:
		o, ok := other.(conditionalExpression)
		if !ok || e.kind != o.kind || e.comparison != o.comparison || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case sequenceCallExpression:
		o, ok := other.(sequenceCallExpression)
		if !ok || e.name != o.name || e.ref != o.ref || e.temporary != o.temporary || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case nullCountExpression:
		o, ok := other.(nullCountExpression)
		if !ok || e.kind != o.kind || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	}
	return false
}
func (b *expressionBinder) rewriteGroup(expr BoundExpr, keys []BoundExpr) (BoundExpr, error) {
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	for i, key := range keys {
		if b.sameGroupExpression(expr, key) {
			properties := expr.Properties()
			properties.RowDependent = false
			properties.StageDependent = true
			info := expressionInfo{usage: expr.ResultUsage(), pos: expr.SourcePosition(), properties: properties}
			return groupExpression{info, i}, nil
		}
	}
	var err error
	rewrite := func(child BoundExpr) BoundExpr {
		if err != nil || child == nil {
			return child
		}
		var out BoundExpr
		out, err = b.rewriteGroup(child, keys)
		return out
	}
	stage := func(info *expressionInfo) { info.properties.StageDependent = true }
	switch e := expr.(type) {
	case aggregateExpression:
		return groupExpression{e.expressionInfo, len(keys) + e.slot}, nil
	case columnExpression:
		name := b.queryNamespace.frame[e.slot].Name
		for _, q := range b.queryNamespace.qualifiers {
			found := false
			for _, c := range q.columns {
				if c.slot == e.slot {
					name = q.name + "." + c.descriptor.Name
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		return nil, expressionError("42803", fmt.Sprintf(`column "%s" must appear in the GROUP BY clause or be used in an aggregate function`, name), e.pos)
	case coercionExpression:
		e.child = rewrite(e.child)
		stage(&e.expressionInfo)
		expr = e
	case arithmeticExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case comparisonExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case logicalExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case nullTestExpression:
		e.child = rewrite(e.child)
		stage(&e.expressionInfo)
		expr = e
	case truthTestExpression:
		e.child = rewrite(e.child)
		stage(&e.expressionInfo)
		expr = e
	case distinctExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case caseExpression:
		e.test = rewrite(e.test)
		e.arms = append([]caseBranch(nil), e.arms...)
		for i := range e.arms {
			e.arms[i].condition = rewrite(e.arms[i].condition)
			e.arms[i].result = rewrite(e.arms[i].result)
		}
		e.otherwise = rewrite(e.otherwise)
		stage(&e.expressionInfo)
		expr = e
	case conditionalExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case sequenceCallExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case nullCountExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	}
	return expr, err
}
func aggregateOccurrence(expr BoundExpr) *aggregateExpression {
	var found *aggregateExpression
	walk := func(child BoundExpr) {
		if found == nil {
			found = aggregateOccurrence(child)
		}
	}
	switch e := expr.(type) {
	case aggregateExpression:
		return &e
	case coercionExpression:
		walk(e.child)
	case arithmeticExpression:
		walk(e.left)
		walk(e.right)
	case comparisonExpression:
		walk(e.left)
		walk(e.right)
	case logicalExpression:
		walk(e.left)
		walk(e.right)
	case nullTestExpression:
		walk(e.child)
	case truthTestExpression:
		walk(e.child)
	case distinctExpression:
		walk(e.left)
		walk(e.right)
	case caseExpression:
		walk(e.test)
		for _, arm := range e.arms {
			walk(arm.condition)
			walk(arm.result)
		}
		walk(e.otherwise)
	case conditionalExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case sequenceCallExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case nullCountExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	}
	return found
}
func groupOrdinal(raw parser.Expr, width int) (int, bool, error) {
	literal, ok := raw.(parser.LiteralExpr)
	if !ok {
		return 0, false, nil
	}
	value := literal.Value
	constant := parser.OrderConstant{Value: parser.Value{Kind: parser.NumericValue, Text: value.Text, Pos: value.Pos}, Real: value.Kind != parser.IntegerValue}
	if value.Kind == parser.IntegerValue {
		constant.Value.Text = strconv.FormatInt(value.Integer, 10)
		if value.Integer < 0 {
			constant.Sign = "-"
			constant.Value.Text = strings.TrimPrefix(constant.Value.Text, "-")
		}
	}
	ordinal, isOrdinal := constant.Ordinal()
	if !isOrdinal {
		return 0, false, expressionError("42601", "non-integer constant in GROUP BY", raw.SourcePosition())
	}
	if ordinal < 1 || ordinal > int64(width) {
		return 0, false, expressionError("42P10", fmt.Sprintf("GROUP BY position %d is not in select list", ordinal), raw.SourcePosition())
	}
	return int(ordinal) - 1, true, nil
}
