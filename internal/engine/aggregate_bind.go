package engine

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Persisted predicates have no query-local window-call metadata. A nil binder
// selects conservative structural comparison without constructing binder state.
func sameGroupExpression(a, other expr.Expr) bool {
	return (*expressionBinder)(nil).sameGroupExpression(a, other)
}

func (b *expressionBinder) sameGroupExpression(a, other expr.Expr) bool {
	return b.sameGroupExpressionIn(a, other, nil)
}

func (b *expressionBinder) sameGroupExpressionIn(a, other expr.Expr, comparison *queryTargetComparison) bool {
	if a == nil || other == nil {
		return a == nil && other == nil
	}
	checkDefinitionEquality := b != nil && b.checkDefinitionEquality
	leftUsage, rightUsage := a.ResultUsage(), other.ResultUsage()
	if checkDefinitionEquality {
		leftUsage.Collation.Derivation = rightUsage.Collation.Derivation
	}
	if !sqltype.SameRecordUsage(leftUsage, rightUsage) || a.CollationIndeterminate() != other.CollationIndeterminate() || !checkDefinitionEquality && (a.ExplicitCollation().Character > 0) != (other.ExplicitCollation().Character > 0) {
		return false
	}
	same := b.sameGroupExpression
	if comparison != nil {
		same = func(a, other expr.Expr) bool { return b.sameGroupExpressionIn(a, other, comparison) }
	}
	switch e := a.(type) {
	case assignmentValueExpression:
		o, ok := other.(assignmentValueExpression)
		if !ok || !same(e.base, o.base) || !same(e.value, o.value) {
			return false
		}
		left, right := e.target, o.target
		for left != nil && right != nil {
			if left.attribute != right.attribute || left.container != right.container || left.destination != right.destination || !left.reapply.Equal(right.reapply) {
				return false
			}
			if left.attribute == 0 && !same(left.node, right.node) {
				return false
			}
			left, right = left.next, right.next
		}
		return left == nil && right == nil
	case tableOIDExpression:
		o, ok := other.(tableOIDExpression)
		return ok && e.owner == o.owner
	case groupingExpression:
		o, ok := other.(groupingExpression)
		if !ok || !comparison.sameFrame(e.frame, o.frame) || len(e.args) != len(o.args) {
			return false
		}
		for i, arg := range e.args {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case rowComparisonExpression:
		o, ok := other.(rowComparisonExpression)
		if !ok || e.operator != o.operator || len(e.comparisons) != len(o.comparisons) || len(e.ordering) != len(o.ordering) {
			return false
		}
		for i, pair := range e.comparisons {
			if !same(pair, o.comparisons[i]) {
				return false
			}
		}
		for i, provider := range e.ordering {
			if !provider.Equal(o.ordering[i]) {
				return false
			}
		}
		return true
	case updateOutputExpression:
		o, ok := other.(updateOutputExpression)
		return ok && e.index == o.index && e.group == o.group
	case subqueryExpression:
		o, ok := other.(subqueryExpression)
		if !ok || e.updateWidth != o.updateWidth || e.kind != o.kind || e.operator != o.operator || e.negate != o.negate || len(e.membership) != len(o.membership) || (comparison != nil && e.presenceOnly != o.presenceOnly) || len(e.comparisons) != len(o.comparisons) {
			return false
		}
		if (e.jsonArray == nil) != (o.jsonArray == nil) {
			return false
		}
		if e.jsonArray != nil && !same(e.jsonArray.input, o.jsonArray.input) {
			return false
		}
		if comparison == nil {
			if e.queryPlan.Root.(*projectNode).frame != o.queryPlan.Root.(*projectNode).frame {
				return false
			}
		} else if !comparison.sameQuery(b, e.queryPlan.Root, o.queryPlan.Root) {
			return false
		}
		if comparison != nil {
			if len(e.ordering) != len(o.ordering) {
				return false
			}
			for i, provider := range e.ordering {
				if !provider.Equal(o.ordering[i]) {
					return false
				}
			}
		}
		for i, c := range e.comparisons {
			if !same(c.left, o.comparisons[i].left) {
				return false
			}
			if comparison != nil && (!c.predicate.Equal(o.comparisons[i].predicate) || !same(c.right, o.comparisons[i].right)) {
				return false
			}
		}
		return true
	case aggregateExpression:
		o, ok := other.(aggregateExpression)
		return ok && e.slot == o.slot && comparison.sameFrame(e.frame, o.frame)
	case *windowExpression:
		o, ok := other.(*windowExpression)
		if !ok || !comparison.sameFrame(e.frame, o.frame) {
			return false
		}
		if e == o {
			return true
		}
		if comparison != nil {
			return e.slot == o.slot
		}
		if b == nil {
			// A call slot alone cannot prove window semantic identity.
			return false
		}
		if e.call < 0 || o.call < 0 || e.call >= len(b.windowCalls) || o.call >= len(b.windowCalls) {
			return e.slot == o.slot
		}
		a, c := b.windowCalls[e.call], b.windowCalls[o.call]
		return b.sameWindowCall(a, c)
	case groupExpression:
		o, ok := other.(groupExpression)
		return ok && e.slot == o.slot && comparison.sameFrame(e.frame, o.frame)
	case literalExpression:
		o, ok := other.(literalExpression)
		if !ok {
			return false
		}
		return e.UnknownNull == o.UnknownNull && sqltype.SameDatum(e.value, o.value)
	case domainValueExpression:
		_, ok := other.(domainValueExpression)
		return ok
	case columnExpression:
		o, ok := other.(columnExpression)
		return ok && e.origin == o.origin && e.scope == o.scope && comparison.sameFrame(e.frame, o.frame) && e.slot == o.slot
	case cachedOrderExpression:
		o, ok := other.(cachedOrderExpression)
		return comparison != nil && ok && e.slot == o.slot
	case parameterExpression:
		o, ok := other.(parameterExpression)
		return ok && e.number == o.number
	case rowExpression:
		o, ok := other.(rowExpression)
		if !ok || len(e.fields) != len(o.fields) || (e.wholeNames == nil) != (o.wholeNames == nil) || e.wholeName != o.wholeName || e.wholeSource != o.wholeSource || e.presenceSlot != o.presenceSlot || e.presenceFrame != o.presenceFrame || e.returningWhole != o.returningWhole || e.returningNull != o.returningNull {
			return false
		}
		if !slices.Equal(e.fieldProjection, o.fieldProjection) {
			return false
		}
		for i, field := range e.fields {
			if !same(field, o.fields[i]) {
				return false
			}
		}
		return true
	case fieldExpression:
		o, ok := other.(fieldExpression)
		return ok && e.attribute == o.attribute && same(e.base, o.base)
	case arrayExpression:
		o, ok := other.(arrayExpression)
		if !ok || e.multidimensional != o.multidimensional || len(e.elements) != len(o.elements) {
			return false
		}
		for i, element := range e.elements {
			if !same(element, o.elements[i]) {
				return false
			}
		}
		return true
	case subscriptExpression:
		o, ok := other.(subscriptExpression)
		if !ok || e.slice != o.slice || len(e.subscripts) != len(o.subscripts) || !same(e.base, o.base) {
			return false
		}
		for i, s := range e.subscripts {
			t := o.subscripts[i]
			if s.slice != t.slice || !same(s.lower, t.lower) || !same(s.upper, t.upper) {
				return false
			}
		}
		return true
	case collationExpression:
		o, ok := other.(collationExpression)
		return ok && same(e.child, o.child)
	case coercionExpression:
		o, ok := other.(coercionExpression)
		return ok && e.plan.Equal(o.plan) && (checkDefinitionEquality || e.mode == o.mode) && same(e.child, o.child)
	case bitwiseExpression:
		o, ok := other.(bitwiseExpression)
		return ok && e.provider == o.provider && e.bits == o.bits && e.network == o.network && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case selectedOperatorExpression:
		o, ok := other.(selectedOperatorExpression)
		return ok && e.ref == o.ref && same(e.provider, o.provider)
	case arithmeticExpression:
		o, ok := other.(arithmeticExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case likeExpression:
		o, ok := other.(likeExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && e.namespace == o.namespace && same(e.left, o.left) && same(e.right, o.right)
	case scalarArrayComparisonExpression:
		o, ok := other.(scalarArrayComparisonExpression)
		if !ok || !e.predicate.Equal(o.predicate) || e.operator != o.operator || e.namespace != o.namespace || e.all != o.all || !same(e.left, o.left) || !same(e.array, o.array) || len(e.items) != len(o.items) {
			return false
		}
		for i, item := range e.items {
			if !same(item, o.items[i]) {
				return false
			}
		}
		return true
	case comparisonExpression:
		o, ok := other.(comparisonExpression)
		return ok && e.predicate.Equal(o.predicate) && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case logicalExpression:
		o, ok := other.(logicalExpression)
		return ok && e.op == o.op && e.queryRight == o.queryRight && same(e.left, o.left) && same(e.right, o.right)
	case nullTestExpression:
		o, ok := other.(nullTestExpression)
		return ok && e.not == o.not && e.whole == o.whole && same(e.child, o.child)
	case truthTestExpression:
		o, ok := other.(truthTestExpression)
		return ok && e.truth == o.truth && e.not == o.not && same(e.child, o.child)
	case distinctExpression:
		o, ok := other.(distinctExpression)
		return ok && e.equality.Equal(o.equality) && e.not == o.not && same(e.left, o.left) && same(e.right, o.right)
	case caseExpression:
		o, ok := other.(caseExpression)
		if !ok || len(e.arms) != len(o.arms) || !same(e.test, o.test) || !same(e.otherwise, o.otherwise) {
			return false
		}
		for i, arm := range e.arms {
			otherArm := o.arms[i]
			if !arm.testPlan.Equal(otherArm.testPlan) || !arm.equality.Equal(otherArm.equality) || arm.testUsage != otherArm.testUsage || !same(arm.condition, otherArm.condition) || !same(arm.result, otherArm.result) {
				return false
			}
		}
		return true
	case conditionalExpression:
		o, ok := other.(conditionalExpression)
		if !ok || e.kind != o.kind || e.missingOrdering != o.missingOrdering || !e.comparison.Equal(o.comparison) || len(e.args) != len(o.args) {
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
	case extensionPathsExpression:
		o, ok := other.(extensionPathsExpression)
		return ok && same(e.arg, o.arg)
	case installedSoundexFunctionExpression:
		o, ok := other.(installedSoundexFunctionExpression)
		if !ok || e.ref != o.ref || e.procedure.Revision() != o.procedure.Revision() || e.Function != o.Function || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case installedMetaphoneFunctionExpression:
		o, ok := other.(installedMetaphoneFunctionExpression)
		if !ok || e.ref != o.ref || e.procedure.Revision() != o.procedure.Revision() || e.Function != o.Function || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case installedLevenshteinFunctionExpression:
		o, ok := other.(installedLevenshteinFunctionExpression)
		if !ok || e.ref != o.ref || e.procedure.Revision() != o.procedure.Revision() || e.Function != o.Function || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case installedUUIDOsspFunctionExpression:
		o, ok := other.(installedUUIDOsspFunctionExpression)
		return ok && e.ref == o.ref && e.procedure == o.procedure && e.Name == o.Name
	case installedCryptoFunctionExpression:
		o, ok := other.(installedCryptoFunctionExpression)
		if !ok || e.ref != o.ref || e.procedure.Revision() != o.procedure.Revision() || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case installedFunctionExpression:
		o, ok := other.(installedFunctionExpression)
		return ok && e.ref == o.ref && e.procedure.Revision() == o.procedure.Revision() && same(e.Arg, o.Arg)
	case expr.GroupIdentity:
		// Function nodes compare their own identity; installed-routine
		// wrappers that embed one keep their explicit cases above.
		return e.SameGroup(other, same, checkDefinitionEquality)
	}
	return false
}
func (b *expressionBinder) rewriteGroup(bound expr.Expr, keys []expr.Expr) (expr.Expr, error) {
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	if operation, ok := bound.(groupingExpression); ok {
		return b.rewriteGrouping(operation, keys)
	}
	for i, key := range keys {
		if b.sameGroupExpression(bound, key) {
			properties := bound.Properties()
			properties.RowDependent = false
			properties.StageDependent = true
			info := expr.InfoOf(bound)
			info.Property = properties
			return groupExpression{Info: info, slot: i, frame: b.frameID()}, nil
		}
	}
	var err error
	rewrite := func(child expr.Expr) expr.Expr {
		if err != nil || child == nil {
			return child
		}
		var out expr.Expr
		out, err = b.rewriteGroup(child, keys)
		return out
	}
	stage := func(info *expr.Info) { info.Property.StageDependent = true }
	switch e := bound.(type) {
	case subqueryExpression:
		old := b.groupSubquery
		b.groupSubquery = true
		root, failure := rewriteQueryExpressions(e.queryPlan.Root, func(child expr.Expr) (expr.Expr, bool, error) {
			if !expressionUsesFrame(child, b.frameID()) {
				return child, false, nil
			}
			next, err := b.rewriteGroup(child, keys)
			return next, true, err
		})
		b.groupSubquery = old
		if failure != nil {
			return nil, failure
		}
		if root != e.queryPlan.Root {
			query := *e.queryPlan
			query.Root = root
			e.queryPlan = &query
		}
		if len(e.comparisons) != 0 {
			comparisons := append([]comparisonExpression(nil), e.comparisons...)
			for i := range comparisons {
				comparisons[i].left = rewrite(comparisons[i].left)
			}
			e.comparisons = comparisons
		}
		bound = e
	case aggregateExpression:
		if e.frame != 0 && e.frame != b.frameID() {
			return e, nil
		}
		return groupExpression{Info: e.Info, slot: len(keys) + e.slot, frame: b.frameID()}, nil
	case *windowExpression:
		return e, nil
	case columnExpression:
		if e.frame != b.frameID() {
			return e, nil
		}
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
		if b.groupSubquery {
			return nil, expr.ErrorAt("42803", fmt.Sprintf(`subquery uses ungrouped column "%s" from outer query`, name), e.Pos)
		}
		return nil, expr.ErrorAt("42803", fmt.Sprintf(`column "%s" must appear in the GROUP BY clause or be used in an aggregate function`, name), e.Pos)
	case rowExpression:
		e, _, err = mapRow(e, func(old expr.Expr) (expr.Expr, bool, error) {
			next := rewrite(old)
			return next, !b.sameGroupExpression(old, next), err
		})
		if err != nil {
			return nil, err
		}
		stage(&e.Info)
		bound = e
	case fieldExpression:
		e.base = rewrite(e.base)
		stage(&e.Info)
		bound = e
	case arrayExpression:
		e.elements = append([]expr.Expr(nil), e.elements...)
		for i, element := range e.elements {
			e.elements[i] = rewrite(element)
		}
		stage(&e.Info)
		bound = e
	case subscriptExpression:
		var failure error
		e, _, failure = mapSubscript(e, func(child expr.Expr) (expr.Expr, bool, error) {
			if child == nil {
				return nil, false, nil
			}
			next := rewrite(child)
			return next, !b.sameGroupExpression(child, next), err
		})
		if failure != nil {
			return nil, failure
		}
		stage(&e.Info)
		bound = e
	case collationExpression:
		e.child = rewrite(e.child)
		stage(&e.Info)
		bound = e
	case coercionExpression:
		e.child = rewrite(e.child)
		stage(&e.Info)
		bound = e
	case bitwiseExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.Info)
		bound = e
	case functions.Timezone:
		e.Value = rewrite(e.Value)
		if !e.Local {
			e.Zone = rewrite(e.Zone)
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case selectedOperatorExpression:
		e.provider = rewrite(e.provider)
		stage(&e.Info)
		bound = e
	case arithmeticExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.Info)
		bound = e
	case likeExpression:
		e.left, e.right = rewrite(e.left), rewrite(e.right)
		stage(&e.Info)
		bound = e
	case rowComparisonExpression:
		e.comparisons = append([]comparisonExpression(nil), e.comparisons...)
		for i, pair := range e.comparisons {
			e.comparisons[i] = rewrite(pair).(comparisonExpression)
		}
		stage(&e.Info)
		bound = e
	case scalarArrayComparisonExpression:
		e.left = rewrite(e.left)
		if e.array != nil {
			e.array = rewrite(e.array)
		}
		e.items = append([]expr.Expr(nil), e.items...)
		for i := range e.items {
			e.items[i] = rewrite(e.items[i])
		}
		stage(&e.Info)
		bound = e
	case comparisonExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.Info)
		bound = e
	case logicalExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.Info)
		bound = e
	case nullTestExpression:
		e.child = rewrite(e.child)
		stage(&e.Info)
		bound = e
	case truthTestExpression:
		e.child = rewrite(e.child)
		stage(&e.Info)
		bound = e
	case distinctExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.Info)
		bound = e
	case caseExpression:
		e.test = rewrite(e.test)
		e.arms = append([]caseBranch(nil), e.arms...)
		for i := range e.arms {
			e.arms[i].condition = rewrite(e.arms[i].condition)
			e.arms[i].result = rewrite(e.arms[i].result)
		}
		e.otherwise = rewrite(e.otherwise)
		stage(&e.Info)
		bound = e
	case conditionalExpression:
		e.args = append([]expr.Expr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case sequenceCallExpression:
		e.args = append([]expr.Expr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case extensionPathsExpression:
		e.arg = rewrite(e.arg)
		stage(&e.Info)
		bound = e
	case installedSoundexFunctionExpression:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case installedMetaphoneFunctionExpression:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case installedLevenshteinFunctionExpression:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case installedUUIDOsspFunctionExpression:
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case installedCryptoFunctionExpression:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case installedFunctionExpression:
		e.Arg = rewrite(e.Arg)
		stage(&e.Info)
		bound = e
	case expr.GroupLeaf:
		// Childless leaf calls keep their metadata.
	case expr.Composite:
		// Function nodes take rewritten children and their grouping-site
		// metadata policy; installed-routine wrappers keep precedence above.
		children := append([]expr.Expr(nil), e.Children()...)
		for i := range children {
			children[i] = rewrite(children[i])
		}
		if err != nil {
			return nil, err
		}
		info := expr.InfoOf(e)
		if hook, ok := e.(expr.GroupPropertyRewriter); ok {
			info.Property = hook.GroupProperties(children)
		} else {
			stage(&info)
		}
		next, failure := expr.Substitute(e, info, children)
		if failure != nil {
			return nil, failure
		}
		bound = next
	}
	return bound, err
}
func aggregateOccurrence(bound expr.Expr) *aggregateExpression {
	var found *aggregateExpression
	walk := func(child expr.Expr) {
		if found == nil {
			found = aggregateOccurrence(child)
		}
	}
	switch e := bound.(type) {
	case rowExpression:
		for _, field := range e.fields {
			walk(field)
		}
	case fieldExpression:
		walk(e.base)
	case arrayExpression:
		for _, element := range e.elements {
			walk(element)
		}
	case subscriptExpression:
		walk(e.base)
		for _, s := range e.subscripts {
			walk(s.lower)
			walk(s.upper)
		}
	case aggregateExpression:
		return &e
	case collationExpression:
		walk(e.child)
	case coercionExpression:
		walk(e.child)
	case bitwiseExpression:
		walk(e.left)
		walk(e.right)
	case functions.Timezone:
		walk(e.Value)
		if !e.Local {
			walk(e.Zone)
		}
	case selectedOperatorExpression:
		walk(e.provider)
	case arithmeticExpression:
		walk(e.left)
		walk(e.right)
	case likeExpression:
		walk(e.left)
		walk(e.right)
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			walk(pair.left)
			walk(pair.right)
		}
	case scalarArrayComparisonExpression:
		walk(e.left)
		if e.array != nil {
			walk(e.array)
		}
		for _, item := range e.items {
			walk(item)
		}
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
	case extensionPathsExpression:
		walk(e.arg)
	case installedSoundexFunctionExpression:
		for _, arg := range e.Args {
			walk(arg)
		}
	case installedMetaphoneFunctionExpression:
		for _, arg := range e.Args {
			walk(arg)
		}
	case installedLevenshteinFunctionExpression:
		for _, arg := range e.Args {
			walk(arg)
		}
	case installedUUIDOsspFunctionExpression:
		// Installed UUID generators and constants are zero-child leaves.
	case installedCryptoFunctionExpression:
		for _, arg := range e.Args {
			walk(arg)
		}
	case installedFunctionExpression:
		walk(e.Arg)
	case expr.Composite:
		// Function nodes list their children in evaluation order.
		for _, child := range e.Children() {
			walk(child)
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
		return 0, false, expr.ErrorAt("42601", "non-integer constant in GROUP BY", raw.SourcePosition())
	}
	if ordinal < 1 || ordinal > int64(width) {
		return 0, false, expr.ErrorAt("42P10", fmt.Sprintf("GROUP BY position %d is not in select list", ordinal), raw.SourcePosition())
	}
	return int(ordinal) - 1, true, nil
}
