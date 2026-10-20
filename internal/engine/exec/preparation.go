package exec

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Preparation is the runtime's route back into statement preparation:
// preflight coercions and domain CHECK programmes reach routine admission and
// the domain catalog, which stay in the engine. Unlike Host it must work
// without a statement scope (DDL admission checks domains with none), so it is
// one stateless package value rather than a host method.
type Preparation interface {
	PreflightCoercions(c *Context, e expr.Expr) (expr.Expr, bool, error)
	CheckDomain(ctx context.Context, c *Context, target *sqltype.Descriptor, value sqltype.Value) error
	// DomainCatalog is the catalog that c's domain checks read: its captured
	// view, else the statement's domain input or resolution catalog.
	DomainCatalog(c *Context) catalog.Read
	// ValidInstalledText reports whether an installed text call still names
	// its captured extension provider.
	ValidInstalledText(n plan.InstalledTextExpr) bool
}

// preparation is installed by the engine at package initialization; until
// then every call fails loudly instead of skipping preparation.
var preparation Preparation = missingPreparation{}

// InstallPreparation sets the preparation value. It runs from init, before
// any statement executes.
func InstallPreparation(p Preparation) { preparation = p }

type missingPreparation struct{}

func (missingPreparation) PreflightCoercions(*Context, expr.Expr) (expr.Expr, bool, error) {
	panic("exec: preparation not installed")
}

func (missingPreparation) CheckDomain(context.Context, *Context, *sqltype.Descriptor, sqltype.Value) error {
	panic("exec: preparation not installed")
}

func (missingPreparation) DomainCatalog(*Context) catalog.Read {
	panic("exec: preparation not installed")
}

func (missingPreparation) ValidInstalledText(plan.InstalledTextExpr) bool {
	panic("exec: preparation not installed")
}
