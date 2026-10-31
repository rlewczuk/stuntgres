package view

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// A normalized action borrows a column default's independent recipe owner, not
// a lexical child of its query. Generic query folding must not borrow the rule
// caller's routine/fold origin for that recipe. This carrier is execution-private;
// the installed action, syntax and catalog output retain their original binding.
type ruleInsertDefaultExpr struct {
	expr.Info
	recipe expr.Expr
	origin analyze.DefaultOrigin
	prepared bool
}

func (n ruleInsertDefaultExpr) Eval(context expr.Context) (sqltype.Value, error) {
	c, ok := context.(*exec.Context)
	if !ok || !n.prepared {
		return sqltype.Value{}, fmt.Errorf("stuntgres: unprepared rule INSERT default")
	}
	return analyze.EvalDefault(c, n.recipe, n.origin)
}
func (ruleInsertDefaultExpr) Children() []expr.Expr { return nil }
func (ruleInsertDefaultExpr) PlanningFold() expr.Fold { return expr.FoldNever }
func (ruleInsertDefaultExpr) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (ruleInsertDefaultExpr) ExecutionDemand() {}
func (n ruleInsertDefaultExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 || info.Usage != n.recipe.ResultUsage() {
		return nil, fmt.Errorf("stuntgres: invalid rule INSERT default recipe")
	}
	n.Info = info
	return n, nil
}
func (n ruleInsertDefaultExpr) SnapshotDemand(literalNull func(expr.Expr) bool) (analysis, text, visibility bool) {
	plan.WalkExpression(n.recipe, func(node expr.Expr) {
		if demand, ok := node.(expr.SnapshotDemander); ok {
			a, t, v := demand.SnapshotDemand(literalNull)
			analysis, text, visibility = analysis || a, text || t, visibility || v
		}
	})
	return
}

// Resolve DEFAULT sentinels against the already refreshed destination/default
// binding. Supplied rows remain shared; only changed cells and their containers
// become private. Identity generation stays with the existing INSERT owner.
func ruleInsertDefaultRows(b analyze.Binding, schema plan.QuerySchema) [][]expr.Expr {
	var rows [][]expr.Expr
	for rowIndex, row := range b.Expressions {
		var values []expr.Expr
		for ordinal, value := range row {
			slot := b.Targets[ordinal]
			discard := len(b.Insert.IdentityDiscard) > slot && b.Insert.IdentityDiscard[slot]
			if value != nil && !discard {
				continue
			}
			var resolved expr.Expr
			if !discard && len(b.Insert.Defaults) > slot && b.Insert.Defaults[slot] != nil {
				recipe := b.Insert.Defaults[slot]
				resolved = ruleInsertDefaultExpr{
					Info: expr.Info{Usage: recipe.ResultUsage(), Pos: recipe.SourcePosition(), Property: recipe.Properties()},
					recipe: recipe, origin: BindingDefaultOrigin(&b, slot),
				}
			} else {
				usage := schema[ordinal].Usage
				resolved = plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: sqltype.Null(usage.Type)}
			}
			if values == nil { values = slices.Clone(row) }
			values[ordinal] = resolved
		}
		if values != nil {
			if rows == nil { rows = slices.Clone(b.Expressions) }
			rows[rowIndex] = values
		}
	}
	if rows == nil { return b.Expressions }
	return rows
}

// PreflightRuleInsertDefaults joins the existing consumed-default preparation
// boundary, after ordinary query demand/preflight. Its recipe is intentionally
// opaque to caller-origin folding, but retains ordinary default-origin planning,
// join admission, runtime evaluation and snapshot demand. Mutation CTEs prepare
// their own command bindings through the same boundary.
func PreflightRuleInsertDefaults(c *exec.Context, b analyze.Binding, root plan.Node) (analyze.Binding, plan.Node, error) {
	if !b.Rule.Template { return b, root, nil }
	if b.Query != nil && root != nil && b.Query.Root != root {
		query := *b.Query
		query.Root = root
		b.Query = &query
	}
	mapper := stored.NewRuleTreeMapper(c.Catalog, nil, 0, b.CTEs, stored.RuleTreeMapping{
		Leaf: func(value expr.Expr) (expr.Expr, bool, error) {
			n, ok := value.(ruleInsertDefaultExpr)
			if !ok || n.prepared { return value, false, nil }
			owner := *c
			analyze.ApplyDefaultOrigin(n.origin, &owner)
			prepared, _, err := exec.PreflightCoercionsWithPolicy(&owner, n.recipe, true)
			if err != nil { return nil, false, err }
			if err := ValidateExpressionJoinExecution(&owner, prepared, c.Catalog); err != nil { return nil, false, err }
			n.recipe, n.prepared = prepared, true
			return n, true, nil
		},
	})
	mapped, err := stored.MapRuleBindingRecipes(b, mapper)
	if err != nil { return b, root, err }
	if root != nil && mapped.Query != nil { root = mapped.Query.Root }
	return mapped, root, nil
}
