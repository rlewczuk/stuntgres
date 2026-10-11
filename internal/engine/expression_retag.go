package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement can retain TEMP sequence builtin generations. Rebind persisted
// scalar trees to those types without evaluating them or changing captured
// values, positions or expression properties.
func retagBoundTypes(view CatalogRead, node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...collationRemapper) (BoundExpr, bool, error) {
	return retagBoundTypesWithQuery(view, node, retainedType, func(root LogicalNode) (LogicalNode, error) {
		if (len(collations) == 0 || collations[0] == nil) && !queryDescriptorsChanged(root, retainedType) {
			return root, nil
		}
		return remapViewQuery(view, root, func(ref ObjectRef) ObjectRef { return ref }, retainedType, collations...)
	}, collations...)
}

func retagBoundTypesWithQuery(view CatalogRead, node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), collations ...collationRemapper) (BoundExpr, bool, error) {
	return mapBoundExpression(view, node, retainedType, queryMapper, nil, collations...)
}

// mapBoundExpression shares the complete copy-on-change traversal with catalog
// retagging. A leaf rewrite runs before children, without evaluating expressions.
func mapBoundExpression(view CatalogRead, node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), leaf func(BoundExpr) (BoundExpr, bool, error), collations ...collationRemapper) (BoundExpr, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	if leaf != nil {
		next, changed, err := leaf(node)
		if changed || err != nil {
			return next, changed, err
		}
	}
	info := boundExpressionInfo(node)
	typ := retainedType(info.usage.Type)
	changed := typ != info.usage.Type
	info.usage.Type = typ
	var err error
	if len(collations) != 0 && collations[0] != nil {
		info.usage.Collation, err = collations[0](info.usage.Collation)
		if err != nil {
			return nil, false, err
		}
		changed = changed || info.usage.Collation != node.ResultUsage().Collation
	}
	child := func(old BoundExpr) BoundExpr {
		if err != nil {
			return old
		}
		var next BoundExpr
		var replaced bool
		next, replaced, err = mapBoundExpression(view, old, retainedType, queryMapper, leaf, collations...)
		changed = changed || replaced
		return next
	}
	var rebound BoundExpr
	switch n := node.(type) {
	case viewQualificationExpression:
		return mapViewQualification(n, func(child BoundExpr) (BoundExpr, bool, error) {
			return mapBoundExpression(view, child, retainedType, queryMapper, leaf, collations...)
		})
	case arrayExpression:
		var elements []BoundExpr
		for i, old := range n.elements {
			next, replaced, failure := mapBoundExpression(view, old, retainedType, queryMapper, leaf, collations...)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if elements == nil {
					elements = append([]BoundExpr(nil), n.elements...)
				}
				elements[i] = next
			}
		}
		if !changed {
			return node, false, err
		}
		if elements != nil {
			n.elements = elements
		}
		n.expressionInfo = info
		rebound = n
	case subscriptExpression:
		var replaced bool
		n, replaced, err = mapSubscript(n, func(old BoundExpr) (BoundExpr, bool, error) {
			return mapBoundExpression(view, old, retainedType, queryMapper, leaf, collations...)
		})
		if err != nil {
			return nil, false, err
		}
		changed = changed || replaced
		n.expressionInfo = info
		rebound = n
	case tableOIDExpression:
		n.expressionInfo = info
		rebound = n
	case indexPropertyExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return nil, false, err
		}
		n.expressionInfo = info
		if n.kind != accessMethodProperty {
			n.oidUsage.Type = retainedType(n.oidUsage.Type)
			n.oidPlan, err = sqltype.PlanCoercion(n.args[0].ResultUsage(), n.oidUsage, sqltype.Implicit)
			if err != nil {
				return nil, false, err
			}
		}
		rebound = n
	case subqueryExpression:
		if queryMapper != nil {
			root, failure := queryMapper(n.queryPlan.Root)
			if failure != nil {
				return nil, false, failure
			}
			if root != n.queryPlan.Root {
				copy := *n.queryPlan
				copy.Root = root
				n.queryPlan = &copy
				changed = true
			}
		}
		var comparisons []comparisonExpression
		for i, old := range n.comparisons {
			next := child(old)
			if err != nil {
				return nil, false, err
			}
			if changed && comparisons == nil {
				comparisons = append([]comparisonExpression(nil), n.comparisons...)
			}
			if comparisons != nil {
				comparisons[i] = next.(comparisonExpression)
			}
		}
		if !changed {
			return node, false, nil
		}
		if comparisons != nil {
			n.comparisons = comparisons
		}
		if len(n.ordering) != 0 {
			n.ordering = make([]sqltype.Comparison, len(n.comparisons))
			for i, c := range n.comparisons {
				n.ordering[i], err = sqltype.BindComparison(c.left.ResultUsage().Type, c.right.ResultUsage().Type)
				if err != nil {
					return nil, false, err
				}
			}
		}
		n.expressionInfo = info
		rebound = n
	case deparseExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case uuidGenerationExpression:
		if len(n.args) != 0 {
			next := child(n.args[0])
			if changed {
				n.args = []BoundExpr{next}
			}
			nextType := retainedType(n.timestamp)
			changed = changed || nextType != n.timestamp
			n.timestamp = nextType
			if err == nil {
				n.addition, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, n.timestamp, n.args[0].ResultUsage().Type, n.timestamp)
			}
		}
		n.expressionInfo = info
		rebound = n
	case randomExpression:
		var args []BoundExpr
		for i, old := range n.args {
			next := child(old)
			if changed && args == nil {
				args = append([]BoundExpr(nil), n.args...)
			}
			if args != nil {
				args[i] = next
			}
		}
		if args != nil {
			n.args = args
		}
		n.expressionInfo = info
		rebound = n
	case advisoryExpression:
		var args []BoundExpr
		for i, old := range n.args {
			next := child(old)
			if changed && args == nil {
				args = append([]BoundExpr(nil), n.args...)
			}
			if args != nil {
				args[i] = next
			}
		}
		if args != nil {
			n.args = args
		}
		n.expressionInfo = info
		rebound = n
	case parameterExpression:
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		rebound = n
	case columnExpression:
		n.expressionInfo = info
		rebound = n
	case groupExpression:
		n.expressionInfo = info
		rebound = n
	case aggregateExpression:
		n.expressionInfo = info
		rebound = n
	case groupingExpression:
		var args []BoundExpr
		for i, arg := range n.args {
			next, replaced, failure := mapBoundExpression(view, arg, retainedType, queryMapper, leaf, collations...)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if args == nil {
					args = append([]BoundExpr(nil), n.args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			n.args = args
		}
		n.expressionInfo = info
		rebound = n
	case *windowExpression:
		copy := *n
		copy.expressionInfo = info
		rebound = &copy
	case cachedOrderExpression:
		n.expressionInfo = info
		rebound = n
	case currentSchemasExpression:
		n.arg = child(n.arg)
		n.expressionInfo = info
		rebound = n
	case sequenceCallExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case binaryExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		var usages [4]sqltype.Usage
		for i := range n.args {
			n.args[i] = child(n.args[i])
			usages[i] = n.args[i].ResultUsage()
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.plan, err = sqltype.BindBinary(n.plan.Name(), info.usage, usages[:len(n.args)]...)
		n.expressionInfo = info
		rebound = n
	case datetimeJustifyExpression:
		n.arg = child(n.arg)
		if err != nil {
			return node, false, err
		}
		plan, failure := n.plan.Retag(retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || plan != n.plan
		if !changed {
			return node, false, nil
		}
		n.plan, n.expressionInfo = plan, info
		rebound = n
	case datetimeExtractExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		plan, failure := n.plan.Retag(retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || plan != n.plan
		if !changed {
			return node, false, nil
		}
		n.plan, n.expressionInfo = plan, info
		rebound = n
	case datetimeOverlapsExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		plan, failure := n.plan.Retag(retainedType)
		if failure != nil {
			return node, false, failure
		}
		for pair, value := range n.folded {
			if value.Type() == nil {
				continue
			}
			retagged, failure := value.Retag(plan.ArgumentType(pair * 2))
			if failure != nil {
				return node, false, failure
			}
			n.folded[pair], changed = retagged, changed || retagged != value
		}
		changed = changed || plan != n.plan
		if !changed {
			return node, false, nil
		}
		n.plan, n.expressionInfo = plan, info
		rebound = n
	case datetimeBinExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		plan, failure := n.plan.Retag(retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || plan != n.plan
		if !changed {
			return node, false, nil
		}
		n.plan, n.expressionInfo = plan, info
		rebound = n
	case datetimeTruncExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		plan, failure := n.plan.Retag(retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || plan != n.plan
		if !changed {
			return node, false, nil
		}
		n.plan, n.expressionInfo = plan, info
		rebound = n
	case datetimeZoneCalendarExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		op := sqltype.ArithmeticAdd
		if n.name == "date_subtract" {
			op = sqltype.ArithmeticSubtract
		}
		provider, failure := sqltype.BindArithmetic(op, n.args[0].ResultUsage().Type, n.args[1].ResultUsage().Type, info.usage.Type)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || provider != n.provider
		if !changed {
			return node, false, nil
		}
		n.provider, n.expressionInfo = provider, info
		rebound = n
	case datetimeAgeFiniteExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		plan, failure := n.plan.Retag(retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || plan != n.plan
		if !changed {
			return node, false, nil
		}
		n.plan = plan
		n.expressionInfo = info
		rebound = n
	case datetimeMakeExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.plan, err = sqltype.BindDatetimeMake(n.plan.Name(), info.usage.Type, n.count)
		n.expressionInfo = info
		rebound = n
	case datetimeFormattingExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.plan, err = n.plan.Retag(info.usage, n.args[0].ResultUsage())
		n.expressionInfo = info
		rebound = n
	case numericFormattingExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.plan, err = n.plan.Retag(info.usage, n.args[0].ResultUsage())
		n.expressionInfo = info
		rebound = n
	case stringExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		var usages [4]sqltype.Usage
		for i := range n.args {
			n.args[i] = child(n.args[i])
			usages[i] = n.args[i].ResultUsage()
		}
		if err != nil {
			return node, false, err
		}
		text := n.plan.TextUsage()
		text.Type = retainedType(text.Type)
		if len(collations) != 0 && collations[0] != nil {
			text.Collation, err = collations[0](text.Collation)
			if err != nil {
				return nil, false, err
			}
		}
		if !changed && text == n.plan.TextUsage() {
			return node, false, nil
		}
		n.plan, err = sqltype.BindString(n.plan.Name(), info.usage, text, usages[:len(n.args)], n.textual, n.plan.CollationIndeterminate())
		n.expressionInfo = info
		rebound = n
	case enumFunctionExpression:
		var args []BoundExpr
		var usages [2]sqltype.Usage
		for i, old := range n.args {
			next, replaced, failure := retagBoundTypesWithQuery(view, old, retainedType, queryMapper, collations...)
			if failure != nil {
				return node, false, failure
			}
			changed = changed || replaced
			if replaced {
				if args == nil {
					args = append([]BoundExpr(nil), n.args...)
				}
				args[i] = next
			}
			usages[i] = next.ResultUsage()
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		if args != nil {
			n.args = args
		}
		n.plan, err = sqltype.BindEnumSupport(n.plan.Name(), info.usage, usages[:len(n.args)]...)
		n.expressionInfo = info
		rebound = n
	case mathExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		var usages [2]sqltype.Usage
		for i := range n.args {
			n.args[i] = child(n.args[i])
			usages[i] = n.args[i].ResultUsage()
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.plan, err = sqltype.BindMath(n.plan.Name(), info.usage, usages[:len(n.args)]...)
		n.expressionInfo = info
		rebound = n
	case visibilityInquiryExpression:
		n.arg = child(n.arg)
		n.expressionInfo = info
		rebound = n
	case commentInquiryExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case nullCountExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case formatExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		int4 := retainedType(n.int4)
		changed = changed || int4 != n.int4
		n.int4 = int4
		n.expressionInfo = info
		rebound = n
	case caseExpression:
		n.test, n.otherwise = child(n.test), child(n.otherwise)
		var arms []caseBranch
		for i, old := range n.arms {
			arm := old
			arm.condition, arm.result = child(arm.condition), child(arm.result)
			if n.test != nil && err == nil {
				arm.testUsage.Type = retainedType(arm.testUsage.Type)
				if len(collations) != 0 && collations[0] != nil {
					arm.testUsage.Collation, err = collations[0](arm.testUsage.Collation)
					if err != nil {
						return nil, false, err
					}
				}
				arm.testPlan, err = sqltype.PlanCoercion(n.test.ResultUsage(), arm.testUsage, sqltype.Implicit)
				if err == nil {
					arm.equality, err = sqltype.BindEqualityComparison(arm.testUsage.Type, arm.condition.ResultUsage().Type)
				}
			}
			if changed && arms == nil {
				arms = append([]caseBranch(nil), n.arms...)
			}
			if arms != nil {
				arms[i] = arm
			}
		}
		if !changed {
			return node, false, err
		}
		if arms != nil {
			n.arms = arms
		}
		n.expressionInfo = info
		rebound = n
	case conditionalExpression:
		var args []BoundExpr
		for i, old := range n.args {
			next := child(old)
			if changed && args == nil {
				args = append([]BoundExpr(nil), n.args...)
			}
			if args != nil {
				args[i] = next
			}
		}
		if !changed {
			return node, false, err
		}
		if args != nil {
			n.args = args
		}
		if err == nil && n.kind != parser.Coalesce {
			left, right := typ, typ
			if n.kind == parser.NullIf {
				left, right = n.args[0].ResultUsage().Type, n.args[1].ResultUsage().Type
			}
			if n.kind == parser.Greatest || n.kind == parser.Least {
				n.comparison, err = bindExtremaComparison(info.usage)
			} else {
				n.comparison, err = sqltype.BindEqualityComparison(left, right)
			}
		}
		n.expressionInfo = info
		rebound = n
	case identityExpression:
		n.expressionInfo = info
		rebound = n
	case currentDateTimeExpression:
		n.expressionInfo = info
		rebound = n
	case transactionTimeExpression:
		n.expressionInfo = info
		rebound = n
	case literalExpression:
		info.unknownNull = n.unknownNull
		info.regclassRef = n.regclassRef
		if !changed {
			return node, false, nil
		}
		var collation collationRemapper
		if len(collations) != 0 {
			collation = collations[0]
		}
		n.value, err = reownDomainDatum(n.value, typ, collation)
		n.expressionInfo = info
		rebound = n
	case timeZoneExpression:
		n.value = child(n.value)
		if !n.local {
			n.zone = child(n.zone)
		}
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case collationExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case coercionExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			if n.mode == sqltype.Implicit && n.child.ResultUsage().Type.BaseType().ID() == sqltype.Char && info.usage.Type.ID() == sqltype.Text {
				n.plan, err = sqltype.PlanExtremaCharText(n.child.ResultUsage(), info.usage)
			} else {
				n.plan, err = sqltype.PlanCoercion(n.child.ResultUsage(), info.usage, n.mode)
			}
		}
		n.expressionInfo = info
		rebound = n
	case bitwiseExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var rt *sqltype.Descriptor
			if n.right != nil {
				rt = n.right.ResultUsage().Type
			}
			n.provider, err = sqltype.BindBitwise(n.operator, n.left.ResultUsage().Type, rt)
		}
		n.expressionInfo = info
		rebound = n
	case arithmeticExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var rightType *sqltype.Descriptor
			if n.right != nil {
				rightType = n.right.ResultUsage().Type
			}
			n.provider, err = sqltype.BindArithmetic(n.operator, n.left.ResultUsage().Type, rightType, info.usage.Type)
		}
		n.expressionInfo = info
		rebound = n
	case likeExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expressionInfo
			collation, err = collatedInfo(view, expressionInfo{usage: n.left.ResultUsage(), pos: info.pos}, n.left, n.right)
			if err == nil {
				n.provider, err = sqltype.BindLike(n.operator, n.left.ResultUsage(), n.right.ResultUsage(), info.usage, collation.collationConflict)
			}
		}
		n.expressionInfo = info
		rebound = n
	case likeEscapeExpression:
		n.pattern, n.escape = child(n.pattern), child(n.escape)
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.pattern, n.escape)
			if err == nil {
				n.provider, err = sqltype.BindLikeEscape(n.pattern.ResultUsage(), n.escape.ResultUsage(), info.usage)
			}
		}
		n.expressionInfo = info
		rebound = n
	case posixExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expressionInfo
			collation, err = collatedInfo(view, expressionInfo{usage: n.left.ResultUsage(), pos: info.pos}, n.left, n.right)
			if err == nil {
				n.provider, err = sqltype.BindPosix(n.operator, n.left.ResultUsage(), n.right.ResultUsage(), info.usage, collation.usage.Collation, collation.collationConflict)
			}
		}
		n.expressionInfo = info
		rebound = n
	case similarExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expressionInfo
			collation, err = collatedInfo(view, expressionInfo{usage: n.left.ResultUsage(), pos: info.pos}, n.left, n.right)
			if err == nil {
				n.provider, err = sqltype.BindSimilar(n.operator, n.left.ResultUsage(), n.right.ResultUsage(), info.usage, collation.usage.Collation, collation.collationConflict)
			}
		}
		n.expressionInfo = info
		rebound = n
	case similarEscapeExpression:
		n.pattern, n.escape = child(n.pattern), child(n.escape)
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.pattern, n.escape)
			if err == nil {
				n.provider, err = sqltype.BindSimilarEscape(n.pattern.ResultUsage(), n.escape.ResultUsage(), info.usage)
			}
		}
		n.expressionInfo = info
		rebound = n
	case posixSubstringExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.args[:]...)
			if err == nil {
				usages := [2]sqltype.Usage{n.args[0].ResultUsage(), n.args[1].ResultUsage()}
				n.provider, err = sqltype.BindPosixSubstring(usages, info.usage, info.usage.Collation, info.collationConflict)
			}
		}
		n.expressionInfo = info
		rebound = n
	case similarSubstringExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.args[:]...)
			if err == nil {
				usages := [3]sqltype.Usage{n.args[0].ResultUsage(), n.args[1].ResultUsage(), n.args[2].ResultUsage()}
				n.provider, err = sqltype.BindSimilarSubstring(usages, info.usage, n.provider.WrapperContext())
			}
		}
		n.expressionInfo = info
		rebound = n
	case comparisonExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			var operator sqltype.ComparisonOperator
			switch n.operator {
			case "=":
				operator = sqltype.CompareEqual
			case "<>":
				operator = sqltype.CompareNotEqual
			case "<":
				operator = sqltype.CompareLess
			case "<=":
				operator = sqltype.CompareLessEqual
			case ">":
				operator = sqltype.CompareGreater
			case ">=":
				operator = sqltype.CompareGreaterEqual
			default:
				return nil, false, fmt.Errorf("stuntgres: invalid default comparison")
			}
			n.predicate, err = sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.right.ResultUsage().Type, typ)
		}
		n.expressionInfo = info
		rebound = n
	case nullTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case truthTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case distinctExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			n.equality, err = sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.left.ResultUsage().Type, n.right.ResultUsage().Type, typ)
		}
		n.expressionInfo = info
		rebound = n
	case logicalExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	default:
		return nil, false, fmt.Errorf("stuntgres: unsupported stored default node")
	}
	if err != nil {
		return nil, false, err
	}
	return rebound, true, nil
}
