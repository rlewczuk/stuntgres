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
	checkDefinitionEquality := b != nil && b.consumer.checkDefinitionEquality
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
		if !ok || !same(e.Base, o.Base) || !same(e.Value, o.Value) {
			return false
		}
		left, right := e.Target, o.Target
		for left != nil && right != nil {
			if left.Attribute != right.Attribute || left.Container != right.Container || left.Destination != right.Destination || !left.Reapply.Equal(right.Reapply) {
				return false
			}
			if left.Attribute == 0 && !same(left.Node, right.Node) {
				return false
			}
			left, right = left.Next, right.Next
		}
		return left == nil && right == nil
	case tableOIDExpression:
		o, ok := other.(tableOIDExpression)
		return ok && e.Owner == o.Owner
	case groupingExpression:
		o, ok := other.(groupingExpression)
		if !ok || !comparison.sameFrame(e.Frame, o.Frame) || len(e.Args) != len(o.Args) {
			return false
		}
		for i, arg := range e.Args {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case rowComparisonExpression:
		o, ok := other.(rowComparisonExpression)
		if !ok || e.Operator != o.Operator || len(e.Comparisons) != len(o.Comparisons) || len(e.Ordering) != len(o.Ordering) {
			return false
		}
		for i, pair := range e.Comparisons {
			if !same(pair, o.Comparisons[i]) {
				return false
			}
		}
		for i, provider := range e.Ordering {
			if !provider.Equal(o.Ordering[i]) {
				return false
			}
		}
		return true
	case updateOutputExpression:
		o, ok := other.(updateOutputExpression)
		return ok && e.Index == o.Index && e.Group == o.Group
	case subqueryExpression:
		o, ok := other.(subqueryExpression)
		if !ok || e.UpdateWidth != o.UpdateWidth || e.Kind != o.Kind || e.Operator != o.Operator || e.Negate != o.Negate || len(e.Membership) != len(o.Membership) || (comparison != nil && e.PresenceOnly != o.PresenceOnly) || len(e.Comparisons) != len(o.Comparisons) {
			return false
		}
		if (e.JSONArray == nil) != (o.JSONArray == nil) {
			return false
		}
		if e.JSONArray != nil && !same(e.JSONArray.Input, o.JSONArray.Input) {
			return false
		}
		if comparison == nil {
			if e.QueryPlan.Root.(*projectNode).Frame != o.QueryPlan.Root.(*projectNode).Frame {
				return false
			}
		} else if !comparison.sameQuery(b, e.QueryPlan.Root, o.QueryPlan.Root) {
			return false
		}
		if comparison != nil {
			if len(e.Ordering) != len(o.Ordering) {
				return false
			}
			for i, provider := range e.Ordering {
				if !provider.Equal(o.Ordering[i]) {
					return false
				}
			}
		}
		for i, c := range e.Comparisons {
			if !same(c.Left, o.Comparisons[i].Left) {
				return false
			}
			if comparison != nil && (!c.Predicate.Equal(o.Comparisons[i].Predicate) || !same(c.Right, o.Comparisons[i].Right)) {
				return false
			}
		}
		return true
	case aggregateExpression:
		o, ok := other.(aggregateExpression)
		return ok && e.Slot == o.Slot && comparison.sameFrame(e.Frame, o.Frame)
	case *windowExpression:
		o, ok := other.(*windowExpression)
		if !ok || !comparison.sameFrame(e.Frame, o.Frame) {
			return false
		}
		if e == o {
			return true
		}
		if comparison != nil {
			return e.Slot == o.Slot
		}
		if b == nil {
			// A call slot alone cannot prove window semantic identity.
			return false
		}
		if e.Call < 0 || o.Call < 0 || e.Call >= len(b.grouping.windowCalls) || o.Call >= len(b.grouping.windowCalls) {
			return e.Slot == o.Slot
		}
		a, c := b.grouping.windowCalls[e.Call], b.grouping.windowCalls[o.Call]
		return b.sameWindowCall(a, c)
	case groupExpression:
		o, ok := other.(groupExpression)
		return ok && e.Slot == o.Slot && comparison.sameFrame(e.Frame, o.Frame)
	case literalExpression:
		o, ok := other.(literalExpression)
		if !ok {
			return false
		}
		return e.UnknownNull == o.UnknownNull && sqltype.SameDatum(e.Value, o.Value)
	case domainValueExpression:
		_, ok := other.(domainValueExpression)
		return ok
	case columnExpression:
		o, ok := other.(columnExpression)
		return ok && e.Origin == o.Origin && e.Scope == o.Scope && comparison.sameFrame(e.Frame, o.Frame) && e.Slot == o.Slot
	case cachedOrderExpression:
		o, ok := other.(cachedOrderExpression)
		return comparison != nil && ok && e.Slot == o.Slot
	case parameterExpression:
		o, ok := other.(parameterExpression)
		return ok && e.Number == o.Number
	case rowExpression:
		o, ok := other.(rowExpression)
		if !ok || len(e.Fields) != len(o.Fields) || (e.WholeNames == nil) != (o.WholeNames == nil) || e.WholeName != o.WholeName || e.WholeSource != o.WholeSource || e.PresenceSlot != o.PresenceSlot || e.PresenceFrame != o.PresenceFrame || e.ReturningWhole != o.ReturningWhole || e.ReturningNull != o.ReturningNull {
			return false
		}
		if !slices.Equal(e.FieldProjection, o.FieldProjection) {
			return false
		}
		for i, field := range e.Fields {
			if !same(field, o.Fields[i]) {
				return false
			}
		}
		return true
	case fieldExpression:
		o, ok := other.(fieldExpression)
		return ok && e.Attribute == o.Attribute && same(e.Base, o.Base)
	case arrayExpression:
		o, ok := other.(arrayExpression)
		if !ok || e.Multidimensional != o.Multidimensional || len(e.Elements) != len(o.Elements) {
			return false
		}
		for i, element := range e.Elements {
			if !same(element, o.Elements[i]) {
				return false
			}
		}
		return true
	case subscriptExpression:
		o, ok := other.(subscriptExpression)
		if !ok || e.Slice != o.Slice || len(e.Subscripts) != len(o.Subscripts) || !same(e.Base, o.Base) {
			return false
		}
		for i, s := range e.Subscripts {
			t := o.Subscripts[i]
			if s.Slice != t.Slice || !same(s.Lower, t.Lower) || !same(s.Upper, t.Upper) {
				return false
			}
		}
		return true
	case collationExpression:
		o, ok := other.(collationExpression)
		return ok && same(e.Child, o.Child)
	case coercionExpression:
		o, ok := other.(coercionExpression)
		return ok && e.Plan.Equal(o.Plan) && (checkDefinitionEquality || e.Mode == o.Mode) && same(e.Child, o.Child)
	case bitwiseExpression:
		o, ok := other.(bitwiseExpression)
		return ok && e.Provider == o.Provider && e.Bits == o.Bits && e.Network == o.Network && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case selectedOperatorExpression:
		o, ok := other.(selectedOperatorExpression)
		return ok && e.Ref == o.Ref && same(e.Provider, o.Provider)
	case arithmeticExpression:
		o, ok := other.(arithmeticExpression)
		return ok && e.Provider == o.Provider && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case likeExpression:
		o, ok := other.(likeExpression)
		return ok && e.Provider == o.Provider && e.Operator == o.Operator && e.Namespace == o.Namespace && same(e.Left, o.Left) && same(e.Right, o.Right)
	case scalarArrayComparisonExpression:
		o, ok := other.(scalarArrayComparisonExpression)
		if !ok || !e.Predicate.Equal(o.Predicate) || e.Operator != o.Operator || e.Namespace != o.Namespace || e.All != o.All || !same(e.Left, o.Left) || !same(e.Array, o.Array) || len(e.Items) != len(o.Items) {
			return false
		}
		for i, item := range e.Items {
			if !same(item, o.Items[i]) {
				return false
			}
		}
		return true
	case comparisonExpression:
		o, ok := other.(comparisonExpression)
		return ok && e.Predicate.Equal(o.Predicate) && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case logicalExpression:
		o, ok := other.(logicalExpression)
		return ok && e.Op == o.Op && e.QueryRight == o.QueryRight && same(e.Left, o.Left) && same(e.Right, o.Right)
	case nullTestExpression:
		o, ok := other.(nullTestExpression)
		return ok && e.Not == o.Not && e.Whole == o.Whole && same(e.Child, o.Child)
	case truthTestExpression:
		o, ok := other.(truthTestExpression)
		return ok && e.Truth == o.Truth && e.Not == o.Not && same(e.Child, o.Child)
	case distinctExpression:
		o, ok := other.(distinctExpression)
		return ok && e.Equality.Equal(o.Equality) && e.Not == o.Not && same(e.Left, o.Left) && same(e.Right, o.Right)
	case caseExpression:
		o, ok := other.(caseExpression)
		if !ok || len(e.Arms) != len(o.Arms) || !same(e.Test, o.Test) || !same(e.Otherwise, o.Otherwise) {
			return false
		}
		for i, arm := range e.Arms {
			otherArm := o.Arms[i]
			if !arm.TestPlan.Equal(otherArm.TestPlan) || !arm.Equality.Equal(otherArm.Equality) || arm.TestUsage != otherArm.TestUsage || !same(arm.Condition, otherArm.Condition) || !same(arm.Result, otherArm.Result) {
				return false
			}
		}
		return true
	case conditionalExpression:
		o, ok := other.(conditionalExpression)
		if !ok || e.Kind != o.Kind || e.MissingOrdering != o.MissingOrdering || !e.Comparison.Equal(o.Comparison) || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
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
			return groupExpression{Info: info, Slot: i, Frame: b.frameID()}, nil
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
		old := b.grouping.groupSubquery
		b.grouping.groupSubquery = true
		root, failure := rewriteQueryExpressions(e.QueryPlan.Root, func(child expr.Expr) (expr.Expr, bool, error) {
			if !expressionUsesFrame(child, b.frameID()) {
				return child, false, nil
			}
			next, err := b.rewriteGroup(child, keys)
			return next, true, err
		})
		b.grouping.groupSubquery = old
		if failure != nil {
			return nil, failure
		}
		if root != e.QueryPlan.Root {
			query := *e.QueryPlan
			query.Root = root
			e.QueryPlan = &query
		}
		if len(e.Comparisons) != 0 {
			comparisons := append([]comparisonExpression(nil), e.Comparisons...)
			for i := range comparisons {
				comparisons[i].Left = rewrite(comparisons[i].Left)
			}
			e.Comparisons = comparisons
		}
		bound = e
	case aggregateExpression:
		if e.Frame != 0 && e.Frame != b.frameID() {
			return e, nil
		}
		return groupExpression{Info: e.Info, Slot: len(keys) + e.Slot, Frame: b.frameID()}, nil
	case *windowExpression:
		return e, nil
	case columnExpression:
		if e.Frame != b.frameID() {
			return e, nil
		}
		name := b.query.namespace.frame[e.Slot].Name
		for _, q := range b.query.namespace.qualifiers {
			found := false
			for _, c := range q.columns {
				if c.slot == e.Slot {
					name = q.name + "." + c.descriptor.Name
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if b.grouping.groupSubquery {
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
		e.Base = rewrite(e.Base)
		stage(&e.Info)
		bound = e
	case arrayExpression:
		e.Elements = append([]expr.Expr(nil), e.Elements...)
		for i, element := range e.Elements {
			e.Elements[i] = rewrite(element)
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
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case coercionExpression:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case bitwiseExpression:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
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
		e.Provider = rewrite(e.Provider)
		stage(&e.Info)
		bound = e
	case arithmeticExpression:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case likeExpression:
		e.Left, e.Right = rewrite(e.Left), rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case rowComparisonExpression:
		e.Comparisons = append([]comparisonExpression(nil), e.Comparisons...)
		for i, pair := range e.Comparisons {
			e.Comparisons[i] = rewrite(pair).(comparisonExpression)
		}
		stage(&e.Info)
		bound = e
	case scalarArrayComparisonExpression:
		e.Left = rewrite(e.Left)
		if e.Array != nil {
			e.Array = rewrite(e.Array)
		}
		e.Items = append([]expr.Expr(nil), e.Items...)
		for i := range e.Items {
			e.Items[i] = rewrite(e.Items[i])
		}
		stage(&e.Info)
		bound = e
	case comparisonExpression:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case logicalExpression:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case nullTestExpression:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case truthTestExpression:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case distinctExpression:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case caseExpression:
		e.Test = rewrite(e.Test)
		e.Arms = append([]caseBranch(nil), e.Arms...)
		for i := range e.Arms {
			e.Arms[i].Condition = rewrite(e.Arms[i].Condition)
			e.Arms[i].Result = rewrite(e.Arms[i].Result)
		}
		e.Otherwise = rewrite(e.Otherwise)
		stage(&e.Info)
		bound = e
	case conditionalExpression:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
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
		for _, field := range e.Fields {
			walk(field)
		}
	case fieldExpression:
		walk(e.Base)
	case arrayExpression:
		for _, element := range e.Elements {
			walk(element)
		}
	case subscriptExpression:
		walk(e.Base)
		for _, s := range e.Subscripts {
			walk(s.Lower)
			walk(s.Upper)
		}
	case aggregateExpression:
		return &e
	case collationExpression:
		walk(e.Child)
	case coercionExpression:
		walk(e.Child)
	case bitwiseExpression:
		walk(e.Left)
		walk(e.Right)
	case functions.Timezone:
		walk(e.Value)
		if !e.Local {
			walk(e.Zone)
		}
	case selectedOperatorExpression:
		walk(e.Provider)
	case arithmeticExpression:
		walk(e.Left)
		walk(e.Right)
	case likeExpression:
		walk(e.Left)
		walk(e.Right)
	case rowComparisonExpression:
		for _, pair := range e.Comparisons {
			walk(pair.Left)
			walk(pair.Right)
		}
	case scalarArrayComparisonExpression:
		walk(e.Left)
		if e.Array != nil {
			walk(e.Array)
		}
		for _, item := range e.Items {
			walk(item)
		}
	case comparisonExpression:
		walk(e.Left)
		walk(e.Right)
	case logicalExpression:
		walk(e.Left)
		walk(e.Right)
	case nullTestExpression:
		walk(e.Child)
	case truthTestExpression:
		walk(e.Child)
	case distinctExpression:
		walk(e.Left)
		walk(e.Right)
	case caseExpression:
		walk(e.Test)
		for _, arm := range e.Arms {
			walk(arm.Condition)
			walk(arm.Result)
		}
		walk(e.Otherwise)
	case conditionalExpression:
		for _, arg := range e.Args {
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
