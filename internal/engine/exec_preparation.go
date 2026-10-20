package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// execPreparation is the runtime's route back into statement preparation:
// preflight coercions and domain CHECK programmes reach routine admission and
// the domain catalog, which stay in the engine. Unlike execHost it must work
// without a statement scope (DDL admission checks domains with none), so it is
// one stateless package value rather than a host method.
type execPreparation interface {
	PreflightCoercions(c *EvalContext, e expr.Expr) (expr.Expr, bool, error)
	CheckDomain(ctx context.Context, c *EvalContext, target *sqltype.Descriptor, value sqltype.Value) error
	// DomainCatalog is the catalog that c's domain checks read: its captured
	// view, else the statement's domain input or resolution catalog.
	DomainCatalog(c *EvalContext) catalog.Read
}

type enginePreparation struct{}

var preparation execPreparation = enginePreparation{}

func (enginePreparation) PreflightCoercions(c *EvalContext, e expr.Expr) (expr.Expr, bool, error) {
	return preflightCoercions(c, e)
}

func (enginePreparation) DomainCatalog(c *EvalContext) catalog.Read {
	return domainCatalog(c)
}
