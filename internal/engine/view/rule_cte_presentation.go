package view

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Mutation-owned WITH bodies are not the INSERT source SELECT's own WITH.
// Retain even unused definitions in the existing analysis-only query carrier so
// current-name/literal presentation and remapping see their ordinary typed graph.
func RetainRuleCTEPresentation(view catalog.Read, b analyze.Binding) analyze.Binding {
	if b.Query == nil || len(b.CTEs) == 0 {
		return b
	}
	project := b.Query.Root.(*plan.ProjectNode)
	var analysis []expr.Expr
	for _, definition := range b.CTEs {
		if definition.Query == nil {
			continue
		}
		found := false
		for _, expr := range project.AnalysisExpressions {
			if query, ok := expr.(plan.SubqueryExpr); ok && query.CTEEdge && query.QueryPlan == definition.Query {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if analysis == nil {
			analysis = slices.Clone(project.AnalysisExpressions)
		}
		typ, _ := view.LookupType(sqltype.Bool)
		analysis = append(analysis, plan.SubqueryExpr{Info: expr.Info{Usage: typ.Usage(), Pos: definition.Position}, QueryPlan: definition.Query, CTEEdge: true})
	}
	if analysis == nil {
		return b
	}
	copy := *project
	copy.AnalysisExpressions = analysis
	query := *b.Query
	query.Root = &copy
	b.Query = &query
	return b
}
