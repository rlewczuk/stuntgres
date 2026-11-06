package stored

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// StatisticsElement uses PostgreSQL's fixed expression presentation: reduced
// parentheses inside the expression, and an outer group unless the accepted
// node is function-shaped. Classification follows the existing FunctionSyntax
// protocol used for index elements, never the rendered SQL's spelling.
func (r Deparser) StatisticsElement(ctx context.Context, node expr.Expr) (string, error) {
	r.Pretty = true
	text, err := r.SQL(ctx, node, DeparseParent{})
	if err != nil {
		return "", err
	}
	bare, err := statisticsFunctionShape(node)
	if err != nil {
		return "", err
	}
	if !bare {
		text = "(" + text + ")"
	}
	return text, nil
}

func statisticsFunctionShape(node expr.Expr) (bool, error) {
	switch n := node.(type) {
	case plan.CoercionExpr:
		if n.XMLSerializeResult {
			return true, nil
		}
		logical, err := n.LogicalInputCast()
		if err != nil || logical.Target.Type != nil {
			return false, err
		}
		if n.Plan.IsJSONReturning() || n.Mode == sqltype.Assignment && !sqlJSONResult(n.Child) {
			return statisticsFunctionShape(n.Child)
		}
		if n.Mode == sqltype.Implicit {
			if _, literal := n.Child.(plan.LiteralExpr); !literal || n.Child.ResultUsage().Type.ID() == n.Usage.Type.ID() {
				return statisticsFunctionShape(n.Child)
			}
		}
		return false, nil
	case functions.Timezone:
		// PostgreSQL lowers AT syntax to a COERCE_SQL_SYNTAX FuncExpr.
		return true, nil
	case functions.JSONPredicate:
		// JsonIsPredicate is not a FuncExpr/JsonExpr, despite its shared hook.
		return false, nil
	case plan.ConditionalExpr, plan.SQLRoutineExpr, plan.RangeConstructorExpr,
		plan.ExtensionPathsExpr, plan.MergeActionExpr,
		plan.InstalledUUIDOsspExpr, plan.InstalledSSLInfoExpr,
		plan.InstalledPgPrewarmExpr,
		plan.InstalledPgFreespaceExpr, plan.InstalledPgvisibilityExpr, plan.InstalledPgBuffercacheEvictionExpr, plan.InstalledPgstattupleExpr,
		plan.InstalledPgBuffercacheAggregateExpr,
		plan.InstalledPgsurgeryExpr,
		plan.InstalledDblinkConnectionExpr,
		plan.InstalledDblinkBuildSQLDeleteExpr,
		plan.InstalledDblinkBuildSQLInsertExpr,
		plan.InstalledDblinkBuildSQLUpdateExpr,
		plan.InstalledPgstattupleStatsExpr, plan.InstalledPageinspectExpr,
		plan.InstalledAmcheckExpr, plan.InstalledEarthExpr, plan.InstalledCubeExpr:
		return true, nil
	case plan.UnnestExpr:
		return false, nil
	}
	if renderable, ok := node.(expr.Renderable); ok {
		spec, err := renderable.Render()
		if err != nil {
			return false, err
		}
		switch spec.Form {
		case expr.FormCall:
			return true, nil
		case expr.FormInfix:
			return false, nil
		}
	}
	if syntax, ok := node.(expr.FunctionSyntax); ok {
		return syntax.LooksLikeFunction(), nil
	}
	return false, nil
}
