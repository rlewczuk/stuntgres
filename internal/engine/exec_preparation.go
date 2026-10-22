package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func init() { exec.InstallPreparation(enginePreparation{}) }

type enginePreparation struct{}

func (enginePreparation) AdmitExpression(c *exec.Context, e expr.Expr, read catalog.Read) error {
	return view.ValidateExpressionJoinExecution(c, e, read)
}

func (enginePreparation) PreflightQuery(c *exec.Context, node plan.Node) (plan.Node, error) {
	return view.PreflightQuery(c, node)
}

func (enginePreparation) PrepareDomainConversion(c *exec.Context, source, target *sqltype.Descriptor) error {
	return prepareDomainConversion(c, source, target)
}

func (enginePreparation) SpecializeRuleParameters(c *exec.Context, e expr.Expr) (expr.Expr, bool, error) {
	return view.SpecializeRuleParameterExpression(c, e)
}

func (enginePreparation) DomainCatalog(c *exec.Context) catalog.Read {
	return domainCatalog(c)
}

func (enginePreparation) ReturnsSet(d *catalog.Procedure) bool { return procedureReturnsSet(d) }

func (enginePreparation) StoredReferenceValid(view catalog.Read, consumer *catalog.Relation, ref ids.ObjectRef) bool {
	return stored.ReferenceEpochValid(view, consumer, ref)
}

func (enginePreparation) ValidInstalledText(n plan.InstalledTextExpr) bool {
	return analyze.ValidInstalledText(n)
}
