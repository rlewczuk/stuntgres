package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func init() { installPreparation(enginePreparation{}) }

type enginePreparation struct{}

func (enginePreparation) PreflightCoercions(c *EvalContext, e expr.Expr) (expr.Expr, bool, error) {
	return preflightCoercions(c, e)
}

func (enginePreparation) DomainCatalog(c *EvalContext) catalog.Read {
	return domainCatalog(c)
}

func (enginePreparation) ValidInstalledText(n plan.InstalledTextExpr) bool {
	return validInstalledText(n)
}
