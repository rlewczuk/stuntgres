package engine

import (
	"fmt"
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
	case subqueryExpression:
		o, ok := other.(subqueryExpression)
		if !ok || e.kind != o.kind || e.operator != o.operator || e.negate != o.negate || len(e.membership) != len(o.membership) || (comparison != nil && e.presenceOnly != o.presenceOnly) || len(e.comparisons) != len(o.comparisons) {
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
	case identityExpression:
		o, ok := other.(identityExpression)
		return ok && e.kind == o.kind && (!checkDefinitionEquality || e.ordinaryCall == o.ordinaryCall)
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
	case objectAddressExpression:
		o, ok := other.(objectAddressExpression)
		if !ok || e.op != o.op || e.slots != o.slots || e.labels != o.labels {
			return false
		}
		for i, arg := range e.args {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case aclValueExpression:
		o, ok := other.(aclValueExpression)
		if !ok || e.kind != o.kind || e.named != o.named || e.namespace != o.namespace {
			return false
		}
		for i, arg := range e.arguments() {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case objectACLExpression:
		o, ok := other.(objectACLExpression)
		if !ok || e.slots != o.slots || e.labels != o.labels {
			return false
		}
		for i, arg := range e.args {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case accessInquiryExpression:
		o, ok := other.(accessInquiryExpression)
		if !ok || e.rowSecurity != o.rowSecurity || e.count != o.count {
			return false
		}
		for i := range e.count {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case namespaceInquiryExpression:
		o, ok := other.(namespaceInquiryExpression)
		return ok && e.kind == o.kind && (e.arg == nil && o.arg == nil || e.arg != nil && o.arg != nil && same(e.arg, o.arg))
	case blockingInquiryExpression:
		o, ok := other.(blockingInquiryExpression)
		return ok && e.kind == o.kind && same(e.arg, o.arg)
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
	case seriesExpression:
		o, ok := other.(seriesExpression)
		if !ok || e.ResultUsage() != o.ResultUsage() || len(e.args) != len(o.args) {
			return false
		}
		for i, arg := range e.args {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case subscriptsExpression:
		o, ok := other.(subscriptsExpression)
		if !ok || e.ResultUsage() != o.ResultUsage() || len(e.args) != len(o.args) {
			return false
		}
		for i, arg := range e.args {
			if !same(arg, o.args[i]) {
				return false
			}
		}
		return true
	case unnestExpression:
		o, ok := other.(unnestExpression)
		return ok && sameIntaggIdentity(e.procedure, e.ref, o.procedure, o.ref) && e.ResultUsage() == o.ResultUsage() && same(e.args[0], o.args[0])
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
	case catalogUserNameExpression:
		o, ok := other.(catalogUserNameExpression)
		return ok && same(e.arg, o.arg)
	case catalogForeignKeysExpression:
		_, ok := other.(catalogForeignKeysExpression)
		return ok
	case transactionIdentityExpression:
		o, ok := other.(transactionIdentityExpression)
		return ok && e.kind == o.kind && ((e.arg == nil && o.arg == nil) || (e.arg != nil && o.arg != nil && same(e.arg, o.arg)))
	case commitTimestampExpression:
		o, ok := other.(commitTimestampExpression)
		return ok && e.kind == o.kind && ((e.arg == nil && o.arg == nil) || (e.arg != nil && o.arg != nil && same(e.arg, o.arg)))
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
	case currentSchemasExpression:
		e.arg = rewrite(e.arg)
		stage(&e.Info)
		bound = e
	case objectAddressExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case aclValueExpression:
		for i := range e.arguments() {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case objectACLExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case accessInquiryExpression:
		for i := range e.count {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case namespaceInquiryExpression:
		if e.arg != nil {
			e.arg = rewrite(e.arg)
		}
		stage(&e.Info)
		bound = e
	case blockingInquiryExpression:
		e.arg = rewrite(e.arg)
		stage(&e.Info)
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
	case functions.LikeEscape:
		e.Pattern, e.Escape = rewrite(e.Pattern), rewrite(e.Escape)
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.Posix:
		e.Left, e.Right = rewrite(e.Left), rewrite(e.Right)
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.Similar:
		e.Left, e.Right = rewrite(e.Left), rewrite(e.Right)
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.SimilarEscape:
		e.Pattern, e.Escape = rewrite(e.Pattern), rewrite(e.Escape)
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.PosixSubstring:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.SimilarSubstring:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case rowComparisonExpression:
		e.comparisons = append([]comparisonExpression(nil), e.comparisons...)
		for i, pair := range e.comparisons {
			e.comparisons[i] = rewrite(pair).(comparisonExpression)
		}
		stage(&e.Info)
		bound = e
	case functions.Regexp:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.RegexpSet:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
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
	case seriesExpression:
		e.args = append([]expr.Expr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case subscriptsExpression:
		e.args = append([]expr.Expr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case unnestExpression:
		e.args = []expr.Expr{rewrite(e.args[0])}
		stage(&e.Info)
		bound = e
	case functions.JSONKeys:
		e.Args = []expr.Expr{rewrite(e.Args[0])}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBRecord:
		args := make([]expr.Expr, len(e.Args))
		for i, arg := range e.Args {
			args[i] = rewrite(arg)
		}
		e.Args = args
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBElements:
		e.Args = []expr.Expr{rewrite(e.Args[0])}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONRecord:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i, arg := range e.Args {
			e.Args[i] = rewrite(arg)
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONEach:
		e.Args = []expr.Expr{rewrite(e.Args[0])}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONElements:
		e.Args = []expr.Expr{rewrite(e.Args[0])}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
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
	case functions.Binary:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeJustify:
		e.Arg = rewrite(e.Arg)
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeExtract:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeOverlaps:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeBin:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeTrunc:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeZoneCalendar:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeAgeFinite:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONStripNulls:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONExtract:
		e, _, err = functions.MapJSONExtractChildren(e, func(arg expr.Expr) (expr.Expr, bool, error) {
			next := rewrite(arg)
			return next, !b.sameGroupExpression(arg, next), err
		})
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBPathQueryArray:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBPathExists:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBPathMatch:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBPathQueryFirst:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBPathQuery:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.JSONBSet:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeMake:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeFormatting:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.NumericFormatting:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.String:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.ArrayRandom:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.ArrayTransform:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.ArrayInspect:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.ArraySearch:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case catalogUserNameExpression:
		e.arg = rewrite(e.arg)
		stage(&e.Info)
		bound = e
	case catalogForeignKeysExpression:
		stage(&e.Info)
		bound = e
	case transactionIdentityExpression:
		if e.arg != nil {
			e.arg = rewrite(e.arg)
		}
		stage(&e.Info)
		bound = e
	case commitTimestampExpression:
		if e.arg != nil {
			e.arg = rewrite(e.arg)
		}
		stage(&e.Info)
		bound = e
	case functions.UUIDExtract:
		e.Arg = rewrite(e.Arg)
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.NetworkFormat:
		for i := range e.Kind.Arity() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.RangeOperator:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.RangeInspect:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.Enum:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.Math:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.UUIDGeneration:
		if len(e.Args) != 0 {
			e.Args = []expr.Expr{rewrite(e.Args[0])}
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.Random:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.DatetimeSleep:
		e.Arg = rewrite(e.Arg)
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case advisoryExpression:
		e.args = append([]expr.Expr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case indexPropertyExpression:
		e.Property = expr.Properties{Volatility: expr.Stable}
		for i := range e.children() {
			e.args[i] = rewrite(e.args[i])
			if err != nil {
				return nil, err
			}
			e.Property = expr.MergeProperties(e.Property, e.args[i].Properties())
		}
		bound = e
	case deparseExpression:
		e.args = append([]expr.Expr(nil), e.args...)
		e.Property = expr.Properties{Volatility: expr.Stable}
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
			if err != nil {
				return nil, err
			}
			e.Property = expr.MergeProperties(e.Property, e.args[i].Properties())
		}
		bound = e
	case visibilityInquiryExpression:
		e.arg = rewrite(e.arg)
		stage(&e.Info)
		bound = e
	case commentInquiryExpression:
		e.args = append([]expr.Expr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.Info)
		bound = e
	case functions.NullCount:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
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
	case functions.SQLJSON:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		stage(&e.Info)
		bound = e
	case functions.JSONPredicate:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case functions.JSONCreate:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.StringVariadic:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case functions.Format:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
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
	case currentSchemasExpression:
		walk(e.arg)
	case objectAddressExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case aclValueExpression:
		for _, arg := range e.arguments() {
			walk(arg)
		}
	case objectACLExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case accessInquiryExpression:
		for _, arg := range e.arguments() {
			walk(arg)
		}
	case namespaceInquiryExpression:
		if e.arg != nil {
			walk(e.arg)
		}
	case blockingInquiryExpression:
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
	case functions.LikeEscape:
		walk(e.Pattern)
		walk(e.Escape)
	case functions.Posix:
		walk(e.Left)
		walk(e.Right)
	case functions.Similar:
		walk(e.Left)
		walk(e.Right)
	case functions.SimilarEscape:
		walk(e.Pattern)
		walk(e.Escape)
	case functions.PosixSubstring:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.SimilarSubstring:
		for _, arg := range e.Args {
			walk(arg)
		}
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			walk(pair.left)
			walk(pair.right)
		}
	case functions.Regexp:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.RegexpSet:
		for _, arg := range e.Args {
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
	case seriesExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case subscriptsExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case unnestExpression:
		walk(e.args[0])
	case functions.JSONKeys:
		walk(e.Args[0])
	case functions.JSONBRecord:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.JSONBElements:
		walk(e.Args[0])
	case functions.JSONRecord:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.JSONEach:
		walk(e.Args[0])
	case functions.JSONElements:
		walk(e.Args[0])
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
	case functions.Binary:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeJustify:
		walk(e.Arg)
	case functions.DatetimeExtract:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeOverlaps:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeBin:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeTrunc:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeZoneCalendar:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeAgeFinite:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONStripNulls:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONExtract:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.JSONBPathQueryArray:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathExists:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathMatch:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathQueryFirst:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathQuery:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBSet:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeMake:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeFormatting:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.NumericFormatting:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.String:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArrayRandom:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArrayTransform:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArrayInspect:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArraySearch:
		for _, arg := range e.Args {
			walk(arg)
		}
	case catalogUserNameExpression:
		walk(e.arg)
	case catalogForeignKeysExpression:
	case transactionIdentityExpression:
		if e.arg != nil {
			walk(e.arg)
		}
	case commitTimestampExpression:
		if e.arg != nil {
			walk(e.arg)
		}
	case functions.UUIDExtract:
		walk(e.Arg)
	case functions.NetworkFormat:
		for _, arg := range e.Args[:e.Kind.Arity()] {
			walk(arg)
		}
	case functions.RangeOperator:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.RangeInspect:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Enum:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Math:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.UUIDGeneration:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Random:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeSleep:
		walk(e.Arg)
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
	case functions.NullCount:
		for _, arg := range e.Args {
			walk(arg)
		}
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
		// Installed random generation is a zero-child leaf.
	case installedCryptoFunctionExpression:
		for _, arg := range e.Args {
			walk(arg)
		}
	case installedFunctionExpression:
		walk(e.Arg)
	case functions.SQLJSON:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.JSONPredicate:
		walk(e.Child)
	case functions.JSONCreate:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.StringVariadic:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Format:
		for _, arg := range e.Args {
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
		return 0, false, expr.ErrorAt("42601", "non-integer constant in GROUP BY", raw.SourcePosition())
	}
	if ordinal < 1 || ordinal > int64(width) {
		return 0, false, expr.ErrorAt("42P10", fmt.Sprintf("GROUP BY position %d is not in select list", ordinal), raw.SourcePosition())
	}
	return int(ordinal) - 1, true, nil
}
