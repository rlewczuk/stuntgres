package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Persisted predicates have no query-local window-call metadata. A nil binder
// selects conservative structural comparison without constructing binder state.
func sameGroupExpression(a, other BoundExpr) bool {
	return (*expressionBinder)(nil).sameGroupExpression(a, other)
}

func (b *expressionBinder) sameGroupExpression(a, other BoundExpr) bool {
	return b.sameGroupExpressionIn(a, other, nil)
}

func (b *expressionBinder) sameGroupExpressionIn(a, other BoundExpr, comparison *queryTargetComparison) bool {
	if a == nil || other == nil {
		return a == nil && other == nil
	}
	checkDefinitionEquality := b != nil && b.checkDefinitionEquality
	leftUsage, rightUsage := a.ResultUsage(), other.ResultUsage()
	if checkDefinitionEquality {
		leftUsage.Collation.Derivation = rightUsage.Collation.Derivation
	}
	if !sameRecordUsage(leftUsage, rightUsage) || a.CollationIndeterminate() != other.CollationIndeterminate() || !checkDefinitionEquality && (a.ExplicitCollation().Character > 0) != (other.ExplicitCollation().Character > 0) {
		return false
	}
	same := b.sameGroupExpression
	if comparison != nil {
		same = func(a, other BoundExpr) bool { return b.sameGroupExpressionIn(a, other, comparison) }
	}
	switch e := a.(type) {
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
			if provider != o.ordering[i] {
				return false
			}
		}
		return true
	case subqueryExpression:
		o, ok := other.(subqueryExpression)
		if !ok || e.kind != o.kind || e.operator != o.operator || e.negate != o.negate || len(e.membership) != len(o.membership) || (comparison != nil && e.presenceOnly != o.presenceOnly) || len(e.comparisons) != len(o.comparisons) {
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
				if provider != o.ordering[i] {
					return false
				}
			}
		}
		for i, c := range e.comparisons {
			if !same(c.left, o.comparisons[i].left) {
				return false
			}
			if comparison != nil && (c.predicate != o.comparisons[i].predicate || !same(c.right, o.comparisons[i].right)) {
				return false
			}
		}
		return true
	case identityExpression:
		o, ok := other.(identityExpression)
		return ok && e.kind == o.kind && (!checkDefinitionEquality || e.ordinaryCall == o.ordinaryCall)
	case currentDateTimeExpression:
		o, ok := other.(currentDateTimeExpression)
		return ok && e.kind == o.kind && e.hasPrecision == o.hasPrecision && (!checkDefinitionEquality || e.precision == o.precision)
	case transactionTimeExpression:
		o, ok := other.(transactionTimeExpression)
		return ok && e.kind == o.kind
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
	case currentSchemasExpression:
		o, ok := other.(currentSchemasExpression)
		return ok && same(e.arg, o.arg)
	case literalExpression:
		o, ok := other.(literalExpression)
		if !ok {
			return false
		}
		return e.unknownNull == o.unknownNull && sqltype.SameDatum(e.value, o.value)
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
		return ok && e.provider == o.provider && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case timeZoneExpression:
		o, ok := other.(timeZoneExpression)
		return ok && e.local == o.local && same(e.value, o.value) && (e.local || same(e.zone, o.zone))
	case arithmeticExpression:
		o, ok := other.(arithmeticExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case likeExpression:
		o, ok := other.(likeExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && e.namespace == o.namespace && same(e.left, o.left) && same(e.right, o.right)
	case likeEscapeExpression:
		o, ok := other.(likeEscapeExpression)
		return ok && e.provider == o.provider && same(e.pattern, o.pattern) && same(e.escape, o.escape)
	case posixExpression:
		o, ok := other.(posixExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case similarExpression:
		o, ok := other.(similarExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case similarEscapeExpression:
		o, ok := other.(similarEscapeExpression)
		return ok && e.provider == o.provider && e.omitted == o.omitted && same(e.pattern, o.pattern) && same(e.escape, o.escape)
	case posixSubstringExpression:
		o, ok := other.(posixSubstringExpression)
		return ok && e.provider == o.provider && same(e.args[0], o.args[0]) && same(e.args[1], o.args[1])
	case similarSubstringExpression:
		o, ok := other.(similarSubstringExpression)
		return ok && e.provider == o.provider && same(e.args[0], o.args[0]) && same(e.args[1], o.args[1]) && same(e.args[2], o.args[2])
	case regexpExpression:
		o, ok := other.(regexpExpression)
		return ok && sameRegexpCall(e.regexpCall, o.regexpCall, same)
	case regexpSetExpression:
		o, ok := other.(regexpSetExpression)
		return ok && sameRegexpCall(e.regexpCall, o.regexpCall, same)
	case scalarArrayComparisonExpression:
		o, ok := other.(scalarArrayComparisonExpression)
		if !ok || e.predicate != o.predicate || e.operator != o.operator || e.namespace != o.namespace || e.all != o.all || !same(e.left, o.left) || !same(e.array, o.array) || len(e.items) != len(o.items) {
			return false
		}
		for i, item := range e.items {
			if !same(item, o.items[i]) {
				return false
			}
		}
		return true
	case jsonKeysExpression:
		o, ok := other.(jsonKeysExpression)
		return ok && e.plan == o.plan && len(e.args) == 1 && len(o.args) == 1 && same(e.args[0], o.args[0])
	case comparisonExpression:
		o, ok := other.(comparisonExpression)
		return ok && e.predicate == o.predicate && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case logicalExpression:
		o, ok := other.(logicalExpression)
		return ok && e.op == o.op && e.queryRight == o.queryRight && same(e.left, o.left) && same(e.right, o.right)
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
		if !ok || e.kind != o.kind || e.missingOrdering != o.missingOrdering || e.comparison != o.comparison || len(e.args) != len(o.args) {
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
	case binaryExpression:
		o, ok := other.(binaryExpression)
		// SQL-special spelling is display provenance, not semantic identity.
		if !ok || e.plan != o.plan || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case datetimeJustifyExpression:
		o, ok := other.(datetimeJustifyExpression)
		return ok && e.plan.Name() == o.plan.Name() && same(e.arg, o.arg)
	case datetimeExtractExpression:
		o, ok := other.(datetimeExtractExpression)
		return ok && e.plan.Name() == o.plan.Name() && e.plan.InputID() == o.plan.InputID() && same(e.args[0], o.args[0]) && same(e.args[1], o.args[1])
	case datetimeOverlapsExpression:
		o, ok := other.(datetimeOverlapsExpression)
		if !ok || e.plan != o.plan || e.wrapperContext != o.wrapperContext {
			return false
		}
		// Folded endpoints are a deterministic derivative of the physical
		// literal arguments below, not another SQL child or signature.
		for i, arg := range e.args {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case datetimeBinExpression:
		o, ok := other.(datetimeBinExpression)
		if !ok || e.plan != o.plan {
			return false
		}
		for i, arg := range e.args {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case datetimeTruncExpression:
		o, ok := other.(datetimeTruncExpression)
		if !ok || e.plan != o.plan || e.count != o.count {
			return false
		}
		for i, arg := range e.children() {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case datetimeZoneCalendarExpression:
		o, ok := other.(datetimeZoneCalendarExpression)
		if !ok || e.name != o.name || e.count != o.count {
			return false
		}
		for i, arg := range e.children() {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case datetimeAgeFiniteExpression:
		o, ok := other.(datetimeAgeFiniteExpression)
		if !ok || e.plan.Name() != o.plan.Name() || e.plan.InputID() != o.plan.InputID() || e.count != o.count {
			return false
		}
		for i, arg := range e.children() {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case datetimeMakeExpression:
		o, ok := other.(datetimeMakeExpression)
		if !ok || e.plan.Name() != o.plan.Name() || e.count != o.count {
			return false
		}
		for i, arg := range e.children() {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case datetimeFormattingExpression:
		o, ok := other.(datetimeFormattingExpression)
		return ok && e.plan.Name() == o.plan.Name() && same(e.args[0], o.args[0]) && same(e.args[1], o.args[1])
	case numericFormattingExpression:
		o, ok := other.(numericFormattingExpression)
		return ok && e.plan.Name() == o.plan.Name() && same(e.args[0], o.args[0]) && same(e.args[1], o.args[1])
	case stringExpression:
		o, ok := other.(stringExpression)
		if !ok || e.plan != o.plan || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case arraySearchExpression:
		o, ok := other.(arraySearchExpression)
		if !ok || e.plan != o.plan || e.namespace != o.namespace || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case enumFunctionExpression:
		o, ok := other.(enumFunctionExpression)
		if !ok || e.plan != o.plan || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case mathExpression:
		o, ok := other.(mathExpression)
		if !ok || e.plan != o.plan || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case uuidGenerationExpression:
		o, ok := other.(uuidGenerationExpression)
		if !ok || e.name != o.name || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case randomExpression:
		o, ok := other.(randomExpression)
		if !ok || e.name != o.name || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case datetimeSleepExpression:
		o, ok := other.(datetimeSleepExpression)
		return ok && e.kind == o.kind && same(e.arg, o.arg)
	case advisoryExpression:
		o, ok := other.(advisoryExpression)
		if !ok || e.name != o.name || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case indexPropertyExpression:
		o, ok := other.(indexPropertyExpression)
		if !ok || e.kind != o.kind {
			return false
		}
		for i, arg := range e.children() {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case deparseExpression:
		o, ok := other.(deparseExpression)
		if !ok || e.signature != o.signature || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case visibilityInquiryExpression:
		o, ok := other.(visibilityInquiryExpression)
		return ok && e.kind == o.kind && same(e.arg, o.arg)
	case commentInquiryExpression:
		o, ok := other.(commentInquiryExpression)
		if !ok || e.kind != o.kind || len(e.args) != len(o.args) {
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
	case installedFunctionExpression:
		o, ok := other.(installedFunctionExpression)
		return ok && e.ref == o.ref && e.procedure.revision == o.procedure.revision && same(e.arg, o.arg)
	case jsonCreateExpression:
		o, ok := other.(jsonCreateExpression)
		if !ok || e.kind != o.kind || e.variadic != o.variadic || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case stringVariadicExpression:
		o, ok := other.(stringVariadicExpression)
		if !ok || e.name != o.name || e.anyelement != o.anyelement || e.variadic != o.variadic || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case formatExpression:
		o, ok := other.(formatExpression)
		if !ok || e.variadic != o.variadic || len(e.args) != len(o.args) {
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
	if operation, ok := expr.(groupingExpression); ok {
		return b.rewriteGrouping(operation, keys)
	}
	for i, key := range keys {
		if b.sameGroupExpression(expr, key) {
			properties := expr.Properties()
			properties.RowDependent = false
			properties.StageDependent = true
			info := boundExpressionInfo(expr)
			info.properties = properties
			return groupExpression{expressionInfo: info, slot: i, frame: b.frameID()}, nil
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
	case subqueryExpression:
		old := b.groupSubquery
		b.groupSubquery = true
		root, failure := rewriteQueryExpressions(e.queryPlan.Root, func(child BoundExpr) (BoundExpr, bool, error) {
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
		expr = e
	case currentSchemasExpression:
		e.arg = rewrite(e.arg)
		stage(&e.expressionInfo)
		expr = e
	case aggregateExpression:
		if e.frame != 0 && e.frame != b.frameID() {
			return e, nil
		}
		return groupExpression{expressionInfo: e.expressionInfo, slot: len(keys) + e.slot, frame: b.frameID()}, nil
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
			return nil, expressionError("42803", fmt.Sprintf(`subquery uses ungrouped column "%s" from outer query`, name), e.pos)
		}
		return nil, expressionError("42803", fmt.Sprintf(`column "%s" must appear in the GROUP BY clause or be used in an aggregate function`, name), e.pos)
	case rowExpression:
		e, _, err = mapRow(e, func(old BoundExpr) (BoundExpr, bool, error) {
			next := rewrite(old)
			return next, !b.sameGroupExpression(old, next), err
		})
		if err != nil {
			return nil, err
		}
		stage(&e.expressionInfo)
		expr = e
	case fieldExpression:
		e.base = rewrite(e.base)
		stage(&e.expressionInfo)
		expr = e
	case arrayExpression:
		e.elements = append([]BoundExpr(nil), e.elements...)
		for i, element := range e.elements {
			e.elements[i] = rewrite(element)
		}
		stage(&e.expressionInfo)
		expr = e
	case subscriptExpression:
		var failure error
		e, _, failure = mapSubscript(e, func(child BoundExpr) (BoundExpr, bool, error) {
			if child == nil {
				return nil, false, nil
			}
			next := rewrite(child)
			return next, !b.sameGroupExpression(child, next), err
		})
		if failure != nil {
			return nil, failure
		}
		stage(&e.expressionInfo)
		expr = e
	case collationExpression:
		e.child = rewrite(e.child)
		stage(&e.expressionInfo)
		expr = e
	case coercionExpression:
		e.child = rewrite(e.child)
		stage(&e.expressionInfo)
		expr = e
	case bitwiseExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case timeZoneExpression:
		e.value = rewrite(e.value)
		if !e.local {
			e.zone = rewrite(e.zone)
		}
		stage(&e.expressionInfo)
		expr = e
	case arithmeticExpression:
		e.left = rewrite(e.left)
		e.right = rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case likeExpression:
		e.left, e.right = rewrite(e.left), rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case likeEscapeExpression:
		e.pattern, e.escape = rewrite(e.pattern), rewrite(e.escape)
		stage(&e.expressionInfo)
		expr = e
	case posixExpression:
		e.left, e.right = rewrite(e.left), rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case similarExpression:
		e.left, e.right = rewrite(e.left), rewrite(e.right)
		stage(&e.expressionInfo)
		expr = e
	case similarEscapeExpression:
		e.pattern, e.escape = rewrite(e.pattern), rewrite(e.escape)
		stage(&e.expressionInfo)
		expr = e
	case posixSubstringExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case similarSubstringExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case rowComparisonExpression:
		e.comparisons = append([]comparisonExpression(nil), e.comparisons...)
		for i, pair := range e.comparisons {
			e.comparisons[i] = rewrite(pair).(comparisonExpression)
		}
		stage(&e.expressionInfo)
		expr = e
	case regexpExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case regexpSetExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case scalarArrayComparisonExpression:
		e.left = rewrite(e.left)
		if e.array != nil {
			e.array = rewrite(e.array)
		}
		e.items = append([]BoundExpr(nil), e.items...)
		for i := range e.items {
			e.items[i] = rewrite(e.items[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case jsonKeysExpression:
		e.args = []BoundExpr{rewrite(e.args[0])}
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
	case binaryExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeJustifyExpression:
		e.arg = rewrite(e.arg)
		stage(&e.expressionInfo)
		expr = e
	case datetimeExtractExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeOverlapsExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeBinExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeTruncExpression:
		for i := range e.children() {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeZoneCalendarExpression:
		for i := range e.children() {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeAgeFiniteExpression:
		for i := range e.children() {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeMakeExpression:
		for i := range e.children() {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeFormattingExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case numericFormattingExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case stringExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case arraySearchExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case enumFunctionExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case mathExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case uuidGenerationExpression:
		if len(e.args) != 0 {
			e.args = []BoundExpr{rewrite(e.args[0])}
		}
		stage(&e.expressionInfo)
		expr = e
	case randomExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case datetimeSleepExpression:
		e.arg = rewrite(e.arg)
		stage(&e.expressionInfo)
		expr = e
	case advisoryExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case indexPropertyExpression:
		e.properties = ExprProperties{Volatility: Stable}
		for i := range e.children() {
			e.args[i] = rewrite(e.args[i])
			if err != nil {
				return nil, err
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
		}
		expr = e
	case deparseExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		e.properties = ExprProperties{Volatility: Stable}
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
			if err != nil {
				return nil, err
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
		}
		expr = e
	case visibilityInquiryExpression:
		e.arg = rewrite(e.arg)
		stage(&e.expressionInfo)
		expr = e
	case commentInquiryExpression:
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
	case installedFunctionExpression:
		e.arg = rewrite(e.arg)
		stage(&e.expressionInfo)
		expr = e
	case jsonCreateExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case stringVariadicExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case formatExpression:
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
	case currentSchemasExpression:
		walk(e.arg)
	case aggregateExpression:
		return &e
	case collationExpression:
		walk(e.child)
	case coercionExpression:
		walk(e.child)
	case bitwiseExpression:
		walk(e.left)
		walk(e.right)
	case timeZoneExpression:
		walk(e.value)
		if !e.local {
			walk(e.zone)
		}
	case arithmeticExpression:
		walk(e.left)
		walk(e.right)
	case likeExpression:
		walk(e.left)
		walk(e.right)
	case likeEscapeExpression:
		walk(e.pattern)
		walk(e.escape)
	case posixExpression:
		walk(e.left)
		walk(e.right)
	case similarExpression:
		walk(e.left)
		walk(e.right)
	case similarEscapeExpression:
		walk(e.pattern)
		walk(e.escape)
	case posixSubstringExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case similarSubstringExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			walk(pair.left)
			walk(pair.right)
		}
	case regexpExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case regexpSetExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case scalarArrayComparisonExpression:
		walk(e.left)
		if e.array != nil {
			walk(e.array)
		}
		for _, item := range e.items {
			walk(item)
		}
	case jsonKeysExpression:
		walk(e.args[0])
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
	case binaryExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case datetimeJustifyExpression:
		walk(e.arg)
	case datetimeExtractExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case datetimeOverlapsExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case datetimeBinExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case datetimeTruncExpression:
		for _, arg := range e.children() {
			walk(arg)
		}
	case datetimeZoneCalendarExpression:
		for _, arg := range e.children() {
			walk(arg)
		}
	case datetimeAgeFiniteExpression:
		for _, arg := range e.children() {
			walk(arg)
		}
	case datetimeMakeExpression:
		for _, arg := range e.children() {
			walk(arg)
		}
	case datetimeFormattingExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case numericFormattingExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case stringExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case arraySearchExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case enumFunctionExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case mathExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case uuidGenerationExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case randomExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case datetimeSleepExpression:
		walk(e.arg)
	case advisoryExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case indexPropertyExpression:
		for _, arg := range e.children() {
			walk(arg)
		}
	case deparseExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case visibilityInquiryExpression:
		walk(e.arg)
	case commentInquiryExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case nullCountExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case installedFunctionExpression:
		walk(e.arg)
	case jsonCreateExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case stringVariadicExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case formatExpression:
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
