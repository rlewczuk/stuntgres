package engine

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SameGroupExpression compares two bound expressions structurally, as GROUP
// BY matching does. Persisted predicates have no query-local window-call
// metadata; a nil binder selects conservative structural comparison without
// constructing binder state.
func SameGroupExpression(a, other expr.Expr) bool {
	return (*expressionBinder)(nil).sameGroupExpression(a, other)
}

func (b *expressionBinder) sameGroupExpression(a, other expr.Expr) bool {
	return b.sameGroupExpressionIn(a, other, nil)
}

func (b *expressionBinder) sameGroupExpressionIn(a, other expr.Expr, comparison *QueryTargetComparison) bool {
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
	case plan.AssignmentValueExpr:
		o, ok := other.(plan.AssignmentValueExpr)
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
	case plan.TableOIDExpr:
		o, ok := other.(plan.TableOIDExpr)
		return ok && e.Owner == o.Owner
	case plan.GroupingExpr:
		o, ok := other.(plan.GroupingExpr)
		if !ok || !comparison.SameFrame(e.Frame, o.Frame) || len(e.Args) != len(o.Args) {
			return false
		}
		for i, arg := range e.Args {
			if !same(arg, o.Args[i]) {
				return false
			}
		}
		return true
	case plan.RowComparisonExpr:
		o, ok := other.(plan.RowComparisonExpr)
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
	case plan.UpdateOutputExpr:
		o, ok := other.(plan.UpdateOutputExpr)
		return ok && e.Index == o.Index && e.Group == o.Group
	case plan.SubqueryExpr:
		o, ok := other.(plan.SubqueryExpr)
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
			if e.QueryPlan.Root.(*plan.ProjectNode).Frame != o.QueryPlan.Root.(*plan.ProjectNode).Frame {
				return false
			}
		} else if !comparison.SameQuery(b, e.QueryPlan.Root, o.QueryPlan.Root) {
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
	case plan.AggregateExpr:
		o, ok := other.(plan.AggregateExpr)
		return ok && e.Slot == o.Slot && comparison.SameFrame(e.Frame, o.Frame)
	case *plan.WindowExpr:
		o, ok := other.(*plan.WindowExpr)
		if !ok || !comparison.SameFrame(e.Frame, o.Frame) {
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
	case plan.GroupExpr:
		o, ok := other.(plan.GroupExpr)
		return ok && e.Slot == o.Slot && comparison.SameFrame(e.Frame, o.Frame)
	case plan.LiteralExpr:
		o, ok := other.(plan.LiteralExpr)
		if !ok {
			return false
		}
		return e.UnknownNull == o.UnknownNull && sqltype.SameDatum(e.Value, o.Value)
	case plan.DomainValueExpr:
		_, ok := other.(plan.DomainValueExpr)
		return ok
	case plan.ColumnExpr:
		o, ok := other.(plan.ColumnExpr)
		return ok && e.Origin == o.Origin && e.Scope == o.Scope && comparison.SameFrame(e.Frame, o.Frame) && e.Slot == o.Slot
	case plan.CachedOrderExpr:
		o, ok := other.(plan.CachedOrderExpr)
		return comparison != nil && ok && e.Slot == o.Slot
	case plan.ParameterExpr:
		o, ok := other.(plan.ParameterExpr)
		return ok && e.Number == o.Number
	case plan.RowExpr:
		o, ok := other.(plan.RowExpr)
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
	case plan.FieldExpr:
		o, ok := other.(plan.FieldExpr)
		return ok && e.Attribute == o.Attribute && same(e.Base, o.Base)
	case plan.ArrayExpr:
		o, ok := other.(plan.ArrayExpr)
		if !ok || e.Multidimensional != o.Multidimensional || len(e.Elements) != len(o.Elements) {
			return false
		}
		for i, element := range e.Elements {
			if !same(element, o.Elements[i]) {
				return false
			}
		}
		return true
	case plan.SubscriptExpr:
		o, ok := other.(plan.SubscriptExpr)
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
	case plan.CollationExpr:
		o, ok := other.(plan.CollationExpr)
		return ok && same(e.Child, o.Child)
	case plan.CoercionExpr:
		o, ok := other.(plan.CoercionExpr)
		return ok && e.Plan.Equal(o.Plan) && (checkDefinitionEquality || e.Mode == o.Mode) && same(e.Child, o.Child)
	case plan.BitwiseExpr:
		o, ok := other.(plan.BitwiseExpr)
		return ok && e.Provider == o.Provider && e.Bits == o.Bits && e.Network == o.Network && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case plan.SelectedOperatorExpr:
		o, ok := other.(plan.SelectedOperatorExpr)
		return ok && e.Ref == o.Ref && same(e.Provider, o.Provider)
	case plan.ArithmeticExpr:
		o, ok := other.(plan.ArithmeticExpr)
		return ok && e.Provider == o.Provider && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case plan.LikeExpr:
		o, ok := other.(plan.LikeExpr)
		return ok && e.Provider == o.Provider && e.Operator == o.Operator && e.Namespace == o.Namespace && same(e.Left, o.Left) && same(e.Right, o.Right)
	case plan.ScalarArrayComparisonExpr:
		o, ok := other.(plan.ScalarArrayComparisonExpr)
		if !ok || !e.Predicate.Equal(o.Predicate) || e.Operator != o.Operator || e.Namespace != o.Namespace || e.All != o.All || !same(e.Left, o.Left) || !same(e.Array, o.Array) || len(e.Items) != len(o.Items) {
			return false
		}
		for i, item := range e.Items {
			if !same(item, o.Items[i]) {
				return false
			}
		}
		return true
	case plan.ComparisonExpr:
		o, ok := other.(plan.ComparisonExpr)
		return ok && e.Predicate.Equal(o.Predicate) && e.Operator == o.Operator && same(e.Left, o.Left) && same(e.Right, o.Right)
	case plan.LogicalExpr:
		o, ok := other.(plan.LogicalExpr)
		return ok && e.Op == o.Op && e.QueryRight == o.QueryRight && same(e.Left, o.Left) && same(e.Right, o.Right)
	case plan.NullTestExpr:
		o, ok := other.(plan.NullTestExpr)
		return ok && e.Not == o.Not && e.Whole == o.Whole && same(e.Child, o.Child)
	case plan.TruthTestExpr:
		o, ok := other.(plan.TruthTestExpr)
		return ok && e.Truth == o.Truth && e.Not == o.Not && same(e.Child, o.Child)
	case plan.DistinctExpr:
		o, ok := other.(plan.DistinctExpr)
		return ok && e.Equality.Equal(o.Equality) && e.Not == o.Not && same(e.Left, o.Left) && same(e.Right, o.Right)
	case plan.CaseExpr:
		o, ok := other.(plan.CaseExpr)
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
	case plan.ConditionalExpr:
		o, ok := other.(plan.ConditionalExpr)
		if !ok || e.Kind != o.Kind || e.MissingOrdering != o.MissingOrdering || !e.Comparison.Equal(o.Comparison) || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case plan.ExtensionPathsExpr:
		o, ok := other.(plan.ExtensionPathsExpr)
		return ok && same(e.Arg, o.Arg)
	case plan.InstalledSoundexExpr:
		o, ok := other.(plan.InstalledSoundexExpr)
		if !ok || e.Ref != o.Ref || e.Procedure.Revision() != o.Procedure.Revision() || e.Function != o.Function || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case plan.InstalledMetaphoneExpr:
		o, ok := other.(plan.InstalledMetaphoneExpr)
		if !ok || e.Ref != o.Ref || e.Procedure.Revision() != o.Procedure.Revision() || e.Function != o.Function || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case plan.InstalledLevenshteinExpr:
		o, ok := other.(plan.InstalledLevenshteinExpr)
		if !ok || e.Ref != o.Ref || e.Procedure.Revision() != o.Procedure.Revision() || e.Function != o.Function || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case plan.InstalledUUIDOsspExpr:
		o, ok := other.(plan.InstalledUUIDOsspExpr)
		return ok && e.Ref == o.Ref && e.Procedure == o.Procedure && e.Name == o.Name
	case plan.InstalledCryptoExpr:
		o, ok := other.(plan.InstalledCryptoExpr)
		if !ok || e.Ref != o.Ref || e.Procedure.Revision() != o.Procedure.Revision() || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
			return false
		}
		for i := range e.Args {
			if !same(e.Args[i], o.Args[i]) {
				return false
			}
		}
		return true
	case plan.InstalledTextExpr:
		o, ok := other.(plan.InstalledTextExpr)
		return ok && e.Ref == o.Ref && e.Procedure.Revision() == o.Procedure.Revision() && same(e.Arg, o.Arg)
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
	if operation, ok := bound.(plan.GroupingExpr); ok {
		return b.rewriteGrouping(operation, keys)
	}
	for i, key := range keys {
		if b.sameGroupExpression(bound, key) {
			properties := bound.Properties()
			properties.RowDependent = false
			properties.StageDependent = true
			info := expr.InfoOf(bound)
			info.Property = properties
			return plan.GroupExpr{Info: info, Slot: i, Frame: b.FrameID()}, nil
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
	case plan.SubqueryExpr:
		old := b.grouping.groupSubquery
		b.grouping.groupSubquery = true
		root, failure := plan.RewriteQueryExpressions(e.QueryPlan.Root, func(child expr.Expr) (expr.Expr, bool, error) {
			if !analyze.ExpressionUsesFrame(child, b.FrameID()) {
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
			comparisons := append([]plan.ComparisonExpr(nil), e.Comparisons...)
			for i := range comparisons {
				comparisons[i].Left = rewrite(comparisons[i].Left)
			}
			e.Comparisons = comparisons
		}
		bound = e
	case plan.AggregateExpr:
		if e.Frame != 0 && e.Frame != b.FrameID() {
			return e, nil
		}
		return plan.GroupExpr{Info: e.Info, Slot: len(keys) + e.Slot, Frame: b.FrameID()}, nil
	case *plan.WindowExpr:
		return e, nil
	case plan.ColumnExpr:
		if e.Frame != b.FrameID() {
			return e, nil
		}
		name := b.query.namespace.Frame[e.Slot].Name
		for _, q := range b.query.namespace.Qualifiers {
			found := false
			for _, c := range q.Columns {
				if c.Slot == e.Slot {
					name = q.Name + "." + c.Descriptor.Name
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
	case plan.RowExpr:
		e, _, err = plan.MapRow(e, func(old expr.Expr) (expr.Expr, bool, error) {
			next := rewrite(old)
			return next, !b.sameGroupExpression(old, next), err
		})
		if err != nil {
			return nil, err
		}
		stage(&e.Info)
		bound = e
	case plan.FieldExpr:
		e.Base = rewrite(e.Base)
		stage(&e.Info)
		bound = e
	case plan.ArrayExpr:
		e.Elements = append([]expr.Expr(nil), e.Elements...)
		for i, element := range e.Elements {
			e.Elements[i] = rewrite(element)
		}
		stage(&e.Info)
		bound = e
	case plan.SubscriptExpr:
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
	case plan.CollationExpr:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case plan.CoercionExpr:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case plan.BitwiseExpr:
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
	case plan.SelectedOperatorExpr:
		e.Provider = rewrite(e.Provider)
		stage(&e.Info)
		bound = e
	case plan.ArithmeticExpr:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case plan.LikeExpr:
		e.Left, e.Right = rewrite(e.Left), rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case plan.RowComparisonExpr:
		e.Comparisons = append([]plan.ComparisonExpr(nil), e.Comparisons...)
		for i, pair := range e.Comparisons {
			e.Comparisons[i] = rewrite(pair).(plan.ComparisonExpr)
		}
		stage(&e.Info)
		bound = e
	case plan.ScalarArrayComparisonExpr:
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
	case plan.ComparisonExpr:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case plan.LogicalExpr:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case plan.NullTestExpr:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case plan.TruthTestExpr:
		e.Child = rewrite(e.Child)
		stage(&e.Info)
		bound = e
	case plan.DistinctExpr:
		e.Left = rewrite(e.Left)
		e.Right = rewrite(e.Right)
		stage(&e.Info)
		bound = e
	case plan.CaseExpr:
		e.Test = rewrite(e.Test)
		e.Arms = append([]plan.CaseBranch(nil), e.Arms...)
		for i := range e.Arms {
			e.Arms[i].Condition = rewrite(e.Arms[i].Condition)
			e.Arms[i].Result = rewrite(e.Arms[i].Result)
		}
		e.Otherwise = rewrite(e.Otherwise)
		stage(&e.Info)
		bound = e
	case plan.ConditionalExpr:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		stage(&e.Info)
		bound = e
	case plan.ExtensionPathsExpr:
		e.Arg = rewrite(e.Arg)
		stage(&e.Info)
		bound = e
	case plan.InstalledSoundexExpr:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case plan.InstalledMetaphoneExpr:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case plan.InstalledLevenshteinExpr:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case plan.InstalledUUIDOsspExpr:
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case plan.InstalledCryptoExpr:
		e.Args = append([]expr.Expr(nil), e.Args...)
		for i := range e.Args {
			e.Args[i] = rewrite(e.Args[i])
		}
		info := expr.InfoOf(e)
		stage(&info)
		e.Info = info
		bound = e
	case plan.InstalledTextExpr:
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
func aggregateOccurrence(bound expr.Expr) *plan.AggregateExpr {
	var found *plan.AggregateExpr
	walk := func(child expr.Expr) {
		if found == nil {
			found = aggregateOccurrence(child)
		}
	}
	switch e := bound.(type) {
	case plan.RowExpr:
		for _, field := range e.Fields {
			walk(field)
		}
	case plan.FieldExpr:
		walk(e.Base)
	case plan.ArrayExpr:
		for _, element := range e.Elements {
			walk(element)
		}
	case plan.SubscriptExpr:
		walk(e.Base)
		for _, s := range e.Subscripts {
			walk(s.Lower)
			walk(s.Upper)
		}
	case plan.AggregateExpr:
		return &e
	case plan.CollationExpr:
		walk(e.Child)
	case plan.CoercionExpr:
		walk(e.Child)
	case plan.BitwiseExpr:
		walk(e.Left)
		walk(e.Right)
	case functions.Timezone:
		walk(e.Value)
		if !e.Local {
			walk(e.Zone)
		}
	case plan.SelectedOperatorExpr:
		walk(e.Provider)
	case plan.ArithmeticExpr:
		walk(e.Left)
		walk(e.Right)
	case plan.LikeExpr:
		walk(e.Left)
		walk(e.Right)
	case plan.RowComparisonExpr:
		for _, pair := range e.Comparisons {
			walk(pair.Left)
			walk(pair.Right)
		}
	case plan.ScalarArrayComparisonExpr:
		walk(e.Left)
		if e.Array != nil {
			walk(e.Array)
		}
		for _, item := range e.Items {
			walk(item)
		}
	case plan.ComparisonExpr:
		walk(e.Left)
		walk(e.Right)
	case plan.LogicalExpr:
		walk(e.Left)
		walk(e.Right)
	case plan.NullTestExpr:
		walk(e.Child)
	case plan.TruthTestExpr:
		walk(e.Child)
	case plan.DistinctExpr:
		walk(e.Left)
		walk(e.Right)
	case plan.CaseExpr:
		walk(e.Test)
		for _, arm := range e.Arms {
			walk(arm.Condition)
			walk(arm.Result)
		}
		walk(e.Otherwise)
	case plan.ConditionalExpr:
		for _, arg := range e.Args {
			walk(arg)
		}
	case plan.ExtensionPathsExpr:
		walk(e.Arg)
	case plan.InstalledSoundexExpr:
		for _, arg := range e.Args {
			walk(arg)
		}
	case plan.InstalledMetaphoneExpr:
		for _, arg := range e.Args {
			walk(arg)
		}
	case plan.InstalledLevenshteinExpr:
		for _, arg := range e.Args {
			walk(arg)
		}
	case plan.InstalledUUIDOsspExpr:
		// Installed UUID generators and constants are zero-child leaves.
	case plan.InstalledCryptoExpr:
		for _, arg := range e.Args {
			walk(arg)
		}
	case plan.InstalledTextExpr:
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
