package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func init() { exec.InstallPreparation(enginePreparation{}) }

type enginePreparation struct{}

func (enginePreparation) PreflightCoercions(c *exec.Context, e expr.Expr) (expr.Expr, bool, error) {
	return preflightCoercions(c, e)
}

func (enginePreparation) DomainCatalog(c *exec.Context) catalog.Read {
	return domainCatalog(c)
}

func (enginePreparation) ValidInstalledText(n plan.InstalledTextExpr) bool {
	return analyze.ValidInstalledText(n)
}
