package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/functions"
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
	case functions.CurrentDateTime[*EvalContext, ExprProperties]:
		o, ok := other.(functions.CurrentDateTime[*EvalContext, ExprProperties])
		return ok && e.Kind == o.Kind && e.HasPrecision == o.HasPrecision && (!checkDefinitionEquality || e.Precision == o.Precision)
	case functions.VersionInformation[*EvalContext, ExprProperties]:
		o, ok := other.(functions.VersionInformation[*EvalContext, ExprProperties])
		return ok && e.Kind == o.Kind
	case functions.TransactionTime[*EvalContext, ExprProperties]:
		o, ok := other.(functions.TransactionTime[*EvalContext, ExprProperties])
		return ok && e.Kind == o.Kind
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
		return e.unknownNull == o.unknownNull && sqltype.SameDatum(e.value, o.value)
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
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Timezone[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Local == o.Local && same(e.Value, o.Value) && (e.Local || same(e.Zone, o.Zone))
	case arithmeticExpression:
		o, ok := other.(arithmeticExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && same(e.left, o.left) && same(e.right, o.right)
	case likeExpression:
		o, ok := other.(likeExpression)
		return ok && e.provider == o.provider && e.operator == o.operator && e.namespace == o.namespace && same(e.left, o.left) && same(e.right, o.right)
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Provider == o.Provider && same(e.Pattern, o.Pattern) && same(e.Escape, o.Escape)
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Posix[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Provider == o.Provider && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Similar[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Provider == o.Provider && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Provider == o.Provider && e.Omitted == o.Omitted && same(e.Pattern, o.Pattern) && same(e.Escape, o.Escape)
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Provider == o.Provider && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Provider == o.Provider && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1]) && same(e.Args[2], o.Args[2])
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Regexp[BoundExpr, *EvalContext, ExprProperties])
		return ok && sameRegexpCall(e.RegexpCall, o.RegexpCall, same)
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties])
		return ok && sameRegexpCall(e.RegexpCall, o.RegexpCall, same)
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
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan == o.Plan && len(e.Args) == 1 && len(o.Args) == 1 && same(e.Args[0], o.Args[0])
	case functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Name != o.Name || e.Unresolved != o.Unresolved || e.Definitions != o.Definitions || !sameRecordUsage(e.ResultUsage(), o.ResultUsage()) || len(e.Args) != len(o.Args) {
			return false
		}
		for i, arg := range e.Args {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan == o.Plan && len(e.Args) == 1 && len(o.Args) == 1 && same(e.Args[0], o.Args[0])
	case functions.JSONRecord[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONRecord[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan.Name() != o.Plan.Name() || e.Plan.BaseIndex() != o.Plan.BaseIndex() || e.Defined != o.Defined || !sameRecordUsage(e.ResultUsage(), o.ResultUsage()) || len(e.Args) != len(o.Args) {
			return false
		}
		for i, arg := range e.Args {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONEach[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan.Name() == o.Plan.Name() && sameRecordUsage(e.ResultUsage(), o.ResultUsage()) && len(e.Args) == 1 && len(o.Args) == 1 && same(e.Args[0], o.Args[0])
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONElements[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan == o.Plan && len(e.Args) == 1 && len(o.Args) == 1 && same(e.Args[0], o.Args[0])
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
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Binary[BoundExpr, *EvalContext, ExprProperties])
		// SQL-special spelling is display provenance, not semantic identity.
		if !ok || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan.Name() == o.Plan.Name() && same(e.Arg, o.Arg)
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan.Name() == o.Plan.Name() && e.Plan.InputID() == o.Plan.InputID() && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan != o.Plan || e.WrapperContext != o.WrapperContext {
			return false
		}
		// Folded endpoints are a deterministic derivative of the physical
		// literal arguments below, not another SQL child or signature.
		for i, arg := range e.Args {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan != o.Plan {
			return false
		}
		for i, arg := range e.Args {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan != o.Plan || e.Count != o.Count {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Name != o.Name || e.Count != o.Count {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan.Name() != o.Plan.Name() || e.Plan.InputID() != o.Plan.InputID() || e.Count != o.Count {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties])
		if !ok {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
			return false
		}
		for i, arg := range e.Args {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties])
		// Redundant VARIADIC is presentation only for this nonvariadic routine.
		if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONBPathExists[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBPathExists[BoundExpr, *EvalContext, ExprProperties])
		// Redundant VARIADIC is presentation only for this nonvariadic routine.
		if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONBPathMatch[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBPathMatch[BoundExpr, *EvalContext, ExprProperties])
		// Redundant VARIADIC is presentation only for this nonvariadic routine.
		if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONBPathQueryFirst[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBPathQueryFirst[BoundExpr, *EvalContext, ExprProperties])
		// Redundant VARIADIC is presentation only for this nonvariadic routine.
		if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONBPathQuery[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBPathQuery[BoundExpr, *EvalContext, ExprProperties])
		// Redundant VARIADIC is presentation only for this nonvariadic routine.
		if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan.Name() != o.Plan.Name() || e.Count != o.Count {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan.Name() != o.Plan.Name() || e.Count != o.Count {
			return false
		}
		for i, arg := range e.Arguments() {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan.Name() == o.Plan.Name() && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan.Name() == o.Plan.Name() && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.String[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Kind != o.Kind || e.ResultUsage() != o.ResultUsage() || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Kind != o.Kind || len(e.Args) != len(o.Args) || !e.Sort.Equal(o.Sort) {
			return false
		}
		for i := range e.Args {
			if !same(e.Formal(i), o.Formal(i)) {
				return false
			}
		}
		return true
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties])
		// Kind identifies the resolved builtin; lexical pg_catalog qualification
		// is presentation provenance, not a different grouping expression.
		if !ok || e.Kind != o.Kind || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties])
		if !ok || !e.Plan.Equal(o.Plan) || e.Namespace != o.Namespace || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
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
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Kind == o.Kind && same(e.Arg, o.Arg)
	case functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Plan == o.Plan && e.Namespace == o.Namespace && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Enum[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Math[BoundExpr, *EvalContext, ExprProperties])
		if !ok || !e.Plan.Equal(o.Plan) || e.WrapperContext != o.WrapperContext || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Name != o.Name || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Random[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Name != o.Name || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties])
		return ok && e.Kind == o.Kind && same(e.Arg, o.Arg)
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
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.NullCount[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Kind != o.Kind || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case installedSoundexFunctionExpression:
		o, ok := other.(installedSoundexFunctionExpression)
		if !ok || e.ref != o.ref || e.procedure.revision != o.procedure.revision || e.Function != o.Function || len(e.Args) != len(o.Args) {
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
		if !ok || e.ref != o.ref || e.procedure.revision != o.procedure.revision || e.Function != o.Function || len(e.Args) != len(o.Args) {
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
		if !ok || e.ref != o.ref || e.procedure.revision != o.procedure.revision || e.Function != o.Function || len(e.Args) != len(o.Args) {
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
		if !ok || e.ref != o.ref || e.procedure.revision != o.procedure.revision || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
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
		return ok && e.ref == o.ref && e.procedure.revision == o.procedure.revision && same(e.arg, o.arg)
	case sqlJSONExpression:
		o, ok := other.(sqlJSONExpression)
		if !ok || e.raw.Kind != o.raw.Kind || e.raw.Absent != o.raw.Absent || e.raw.Unique != o.raw.Unique || len(e.args) != len(o.args) {
			return false
		}
		for i := range e.args {
			if !same(e.args[i], o.args[i]) {
				return false
			}
		}
		return true
	case jsonPredicateExpression:
		o, ok := other.(jsonPredicateExpression)
		return ok && e.kind == o.kind && e.unique == o.unique && e.not == o.not && same(e.child, o.child)
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Kind != o.Kind || e.Variadic != o.Variadic || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		o, ok := other.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin])
		if !ok || e.Name != o.Name || e.AnyElement != o.AnyElement || e.Variadic != o.Variadic || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		o, ok := other.(functions.Format[BoundExpr, *EvalContext, ExprProperties])
		if !ok || e.Variadic != o.Variadic || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
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
	case objectAddressExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case aclValueExpression:
		for i := range e.arguments() {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case objectACLExpression:
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case accessInquiryExpression:
		for i := range e.count {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case namespaceInquiryExpression:
		if e.arg != nil {
			e.arg = rewrite(e.arg)
		}
		stage(&e.expressionInfo)
		expr = e
	case blockingInquiryExpression:
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
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		e.Value = rewrite(e.Value)
		if !e.Local {
			e.Zone = rewrite(e.Zone)
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
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
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		e.Pattern, e.Escape = rewrite(e.Pattern), rewrite(e.Escape)
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		e.Left, e.Right = rewrite(e.Left), rewrite(e.Right)
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		e.Left, e.Right = rewrite(e.Left), rewrite(e.Right)
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		e.Pattern, e.Escape = rewrite(e.Pattern), rewrite(e.Escape)
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case rowComparisonExpression:
		e.comparisons = append([]comparisonExpression(nil), e.comparisons...)
		for i, pair := range e.comparisons {
			e.comparisons[i] = rewrite(pair).(comparisonExpression)
		}
		stage(&e.expressionInfo)
		expr = e
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
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
	case seriesExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case subscriptsExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case unnestExpression:
		e.args = []BoundExpr{rewrite(e.args[0])}
		stage(&e.expressionInfo)
		expr = e
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = []BoundExpr{rewrite(e.Args[0])}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties]:
		args := make([]BoundExpr, len(e.Args))
		for i, arg := range e.Args {
			args[i] = rewrite(arg)
		}
		e.Args = args
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = []BoundExpr{rewrite(e.Args[0])}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONRecord[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i, arg := range e.Args {
			e.Args[i] = rewrite(arg)
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = []BoundExpr{rewrite(e.Args[0])}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = []BoundExpr{rewrite(e.Args[0])}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
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
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		e.Arg = rewrite(e.Arg)
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		e, _, err = mapJSONExtractChildren(e, func(arg BoundExpr) (BoundExpr, bool, error) {
			next := rewrite(arg)
			return next, !b.sameGroupExpression(arg, next), err
		})
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBPathExists[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBPathMatch[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBPathQueryFirst[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBPathQuery[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Arguments() {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case catalogUserNameExpression:
		e.arg = rewrite(e.arg)
		stage(&e.expressionInfo)
		expr = e
	case catalogForeignKeysExpression:
		stage(&e.expressionInfo)
		expr = e
	case transactionIdentityExpression:
		if e.arg != nil {
			e.arg = rewrite(e.arg)
		}
		stage(&e.expressionInfo)
		expr = e
	case commitTimestampExpression:
		if e.arg != nil {
			e.arg = rewrite(e.arg)
		}
		stage(&e.expressionInfo)
		expr = e
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		e.Arg = rewrite(e.Arg)
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties]:
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		if len(e.Args) != 0 {
			e.Args = []BoundExpr{rewrite(e.Args[0])}
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		e.Arg = rewrite(e.Arg)
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
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
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case installedSoundexFunctionExpression:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case installedMetaphoneFunctionExpression:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case installedLevenshteinFunctionExpression:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case installedUUIDOsspFunctionExpression:
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case installedCryptoFunctionExpression:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case installedFunctionExpression:
		e.arg = rewrite(e.arg)
		stage(&e.expressionInfo)
		expr = e
	case sqlJSONExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i := range e.args {
			e.args[i] = rewrite(e.args[i])
		}
		stage(&e.expressionInfo)
		expr = e
	case jsonPredicateExpression:
		e.child = rewrite(e.child)
		stage(&e.expressionInfo)
		expr = e
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
		expr = e
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := boundExpressionInfo(e)
		stage(&info)
		e.Info = functionInfo(info)
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
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Value)
		if !e.Local {
			walk(e.Zone)
		}
	case arithmeticExpression:
		walk(e.left)
		walk(e.right)
	case likeExpression:
		walk(e.left)
		walk(e.right)
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Pattern)
		walk(e.Escape)
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Left)
		walk(e.Right)
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Left)
		walk(e.Right)
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Pattern)
		walk(e.Escape)
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			walk(pair.left)
			walk(pair.right)
		}
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
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
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Args[0])
	case functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Args[0])
	case functions.JSONRecord[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Args[0])
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
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
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Arg)
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathExists[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathMatch[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathQueryFirst[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBPathQuery[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			walk(arg)
		}
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
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
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Arg)
	case functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
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
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
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
		walk(e.arg)
	case sqlJSONExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case jsonPredicateExpression:
		walk(e.child)
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
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
		return 0, false, expressionError("42601", "non-integer constant in GROUP BY", raw.SourcePosition())
	}
	if ordinal < 1 || ordinal > int64(width) {
		return 0, false, expressionError("42P10", fmt.Sprintf("GROUP BY position %d is not in select list", ordinal), raw.SourcePosition())
	}
	return int(ordinal) - 1, true, nil
}
