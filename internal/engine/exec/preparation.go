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
	// PreflightCoercionsWithPolicy is PreflightCoercions with the constant
	// planning policy: decisiveDefaults is true for consumed stored defaults,
	// CHECK rows and standalone recipes.
	PreflightCoercionsWithPolicy(c *Context, e expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error)
	// AdmitExpression is planning-time execution admission of a prepared
	// expression: routine EXECUTE, virtual generation and subquery join
	// legality, read through view.
	AdmitExpression(c *Context, e expr.Expr, view catalog.Read) error
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

// PreflightCoercionsWithPolicy runs the installed coercion preflight with a
// constant planning policy.
func PreflightCoercionsWithPolicy(c *Context, e expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	return preparation.PreflightCoercionsWithPolicy(c, e, decisiveDefaults)
}

// AdmitExpression runs the installed execution admission of e.
func AdmitExpression(c *Context, e expr.Expr, view catalog.Read) error {
	return preparation.AdmitExpression(c, e, view)
}

type missingPreparation struct{}

func (missingPreparation) PreflightCoercions(*Context, expr.Expr) (expr.Expr, bool, error) {
	panic("exec: preparation not installed")
}

func (missingPreparation) PreflightCoercionsWithPolicy(*Context, expr.Expr, bool) (expr.Expr, bool, error) {
	panic("exec: preparation not installed")
}

func (missingPreparation) AdmitExpression(*Context, expr.Expr, catalog.Read) error {
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
