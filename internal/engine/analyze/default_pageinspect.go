package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

// ClosedPageinspectDefault admits only the genuine scalar splitter's intrinsic
// live-schema dependence. Its arguments retain the existing closed DEFAULT
// rules; no caller frame, parameter or unrelated stage-dependent producer enters
// through this boundary. The expression remains VOLATILE and never folded.
func ClosedPageinspectDefault(node expr.Expr) bool {
	valid, contains := closedPageinspectDefaultTree(node)
	return valid && contains
}

func closedPageinspectDefaultTree(node expr.Expr) (bool, bool) {
	if node == nil {
		return false, false
	}
	properties := node.Properties()
	if properties.RowDependent || properties.ParameterDependent {
		return false, false
	}
	if call, ok := node.(plan.InstalledPageinspectExpr); ok {
		if !validPageinspectSplit(call) {
			return false, false
		}
		for _, child := range call.Args {
			if !closedPageinspectArgument(child) {
				return false, false
			}
		}
		return true, true
	}
	if !pageinspectStageTransparent(node) {
		return closedPageinspectArgument(node), false
	}
	valid, contains := true, false
	plan.ExpressionChildren(node, func(child expr.Expr) {
		childValid, childContains := closedPageinspectDefaultTree(child)
		valid = valid && childValid
		contains = contains || childContains
	})
	return valid && properties.StageDependent == contains, contains
}

func closedPageinspectArgument(node expr.Expr) bool {
	if node == nil {
		return false
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	return p.Volatility <= expr.Stable || ClosedSequenceDefault(node) || ClosedUUIDDefault(node) || ClosedTransactionTimeDefault(node) || ClosedPgcryptoDefault(node) || ClosedSQLRoutineDefault(node)
}

// PageinspectStoredStage proves the origin of every stage-dependent contribution
// in an owning stored scalar tree. It grants no row, parameter, type or routine
// authority: the existing owning renderer still validates each of those.
func PageinspectStoredStage(node expr.Expr) bool {
	if node == nil || node.Properties().ParameterDependent {
		return false
	}
	if call, ok := node.(plan.InstalledPageinspectExpr); ok {
		if !validPageinspectSplit(call) {
			return false
		}
		valid := true
		for _, child := range call.Args {
			valid = PageinspectStoredStage(child) && valid
		}
		return valid
	}
	if !pageinspectStageTransparent(node) {
		return !node.Properties().StageDependent
	}
	valid, stage := true, false
	plan.ExpressionChildren(node, func(child expr.Expr) {
		valid = PageinspectStoredStage(child) && valid
		stage = stage || child.Properties().StageDependent
	})
	return valid && node.Properties().StageDependent == stage
}

func pageinspectStageTransparent(node expr.Expr) bool {
	switch node.(type) {
	case plan.CoercionExpr, plan.CollationExpr, plan.RowExpr, plan.FieldExpr,
		plan.ArrayExpr, plan.SubscriptExpr, plan.ComparisonExpr, plan.ArithmeticExpr,
		plan.BitwiseExpr, plan.LikeExpr, plan.NullTestExpr, plan.TruthTestExpr,
		plan.LogicalExpr, plan.DistinctExpr, plan.ConditionalExpr, plan.CaseExpr,
		plan.ScalarArrayComparisonExpr:
		return true
	}
	return false
}

func validPageinspectSplit(n plan.InstalledPageinspectExpr) bool {
	properties := n.Properties()
	if !properties.StageDependent || properties.Volatility != expr.Volatile {
		return false
	}
	p := n.Procedure
	if n.Member != "tuple_data_split" || p == nil || n.RoutineReference != p || n.Ref.Address != p.Address() || n.Ref.Epoch == 0 || extension.PageinspectProviderOf(p) != extension.StockPageinspect || !p.Origin.Valid(p.Address()) || p.Origin.Installation.Implementation != extension.StockPageinspect || n.ValidateArguments(n.Args) != nil {
		return false
	}
	slot := p.Origin.Declaration
	if slot != 17 && slot != 18 {
		return false
	}
	arity := 5
	if slot == 18 {
		arity = 6
	}
	member := extension.PageinspectMembers[slot]
	return len(n.Args) == arity && p.Signature == member.Signature && p.Result == 1001 && !p.Strict && p.Volatility == expr.Volatile && p.Installed == member.Declaration && p.Display == (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "tuple_data_split", Binary: "$libdir/pageinspect", HasBinary: true})
}
