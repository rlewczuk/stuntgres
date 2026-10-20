package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Mutation-owned WITH bodies are not the INSERT source SELECT's own WITH.
// Retain even unused definitions in the existing analysis-only query carrier so
// current-name/literal presentation and remapping see their ordinary typed graph.
func retainRuleCTEPresentation(view catalog.Read, b binding) binding {
	if b.query == nil || len(b.ctes) == 0 {
		return b
	}
	project := b.query.Root.(*projectNode)
	var analysis []expr.Expr
	for _, definition := range b.ctes {
		if definition.Query == nil {
			continue
		}
		found := false
		for _, expr := range project.AnalysisExpressions {
			if query, ok := expr.(subqueryExpression); ok && query.cteDefinition && query.QueryPlan == definition.Query {
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
		analysis = append(analysis, subqueryExpression{Info: expr.Info{Usage: typ.Usage(), Pos: definition.Position}, QueryPlan: definition.Query, cteDefinition: true})
	}
	if analysis == nil {
		return b
	}
	copy := *project
	copy.AnalysisExpressions = analysis
	query := *b.query
	query.Root = &copy
	b.query = &query
	return b
}
