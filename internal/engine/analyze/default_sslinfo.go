package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// ValidInstalledSSLInfoShape checks the captured declaration and zero-child
// recipe. Catalog membership and current generation are validated by the owner.
func ValidInstalledSSLInfoShape(n plan.InstalledSSLInfoExpr) bool {
	if n.Procedure == nil || extension.SSLInfoProviderOf(n.Procedure) != extension.StockSSLInfo || n.RoutineReference != n.Procedure || n.Ref.Domain() == nil || n.Ref.Epoch == 0 || n.Ref.Address != n.Procedure.Address() || n.Ref.Address.ClassID != ids.ProcedureClassID || n.Ref.Address.SubID != 0 || n.Procedure.Signature != "" || n.Validate() != nil {
		return false
	}
	origin := n.Procedure.Origin
	if !origin.Valid(n.Ref.Address) || origin.Installation.Implementation != extension.StockSSLInfo || origin.Installation.Name() != "sslinfo" || origin.Installation.Version != "1.2" || origin.Declaration >= len(functions.SSLInfoMembers) || functions.SSLInfoMembers[origin.Declaration].Name != n.Name {
		return false
	}
	properties := n.Properties()
	properties.StageDependent = false
	return properties == (expr.Properties{Volatility: expr.Volatile})
}

// ClosedSSLInfoDefault authenticates installed zero-argument calls, not their
// constant non-TLS outputs. It never admits a bare carrier or unrelated volatile
// producer through an otherwise transparent scalar expression.
func ClosedSSLInfoDefault(node expr.Expr) bool {
	valid, contains := closedSSLInfoDefaultTree(node)
	return valid && contains
}

func closedSSLInfoDefaultTree(node expr.Expr) (bool, bool) {
	if node == nil { return true, false }
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent { return false, false }
	switch n := node.(type) {
	case plan.InstalledSSLInfoExpr:
		return ValidInstalledSSLInfoShape(n), true
	case functions.UUIDGeneration, plan.InstalledUUIDOsspExpr:
		return ClosedUUIDDefault(node), false
	case functions.SequenceCall:
		return ClosedSequenceDefault(node), false
	case functions.TransactionTime, functions.DatetimeSleep:
		return ClosedTransactionTimeDefault(node), false
	case plan.InstalledCryptoExpr:
		return ClosedPgcryptoDefault(node), false
	case plan.SQLRoutineExpr, plan.RangeConstructorExpr:
		return ClosedSQLRoutineDefault(node), false
	case plan.CoercionExpr, plan.CollationExpr, plan.RowExpr, plan.FieldExpr,
		plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr,
		plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr,
		plan.ConditionalExpr, plan.CaseExpr, plan.ScalarArrayComparisonExpr:
	case expr.ClosureTransparent:
	default:
		return p.Volatility <= expr.Stable, false
	}
	valid, contains := true, false
	plan.ExpressionChildren(node, func(child expr.Expr) {
		childValid, childContains := closedSSLInfoDefaultTree(child)
		valid = valid && childValid
		contains = contains || childContains
	})
	return valid, contains
}
