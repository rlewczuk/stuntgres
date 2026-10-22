package exec

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Preparation is the runtime's route back into statement preparation:
// coercion preflight and domain CHECK programmes reach query preflight, rule
// parameter specialization, the domain programme state and the domain
// catalog, which stay in the engine. Unlike Host it must work without a
// statement scope (DDL admission checks domains with none), so it is one
// stateless package value rather than a host method.
type Preparation interface {
	// AdmitExpression is planning-time execution admission of a prepared
	// expression: routine EXECUTE, virtual generation and subquery join
	// legality, read through view.
	AdmitExpression(c *Context, e expr.Expr, view catalog.Read) error
	// PreflightQuery preflights a subquery's plan tree: query preflight
	// reaches the engine's retag, rule producer demand and window pruning.
	PreflightQuery(c *Context, node plan.Node) (plan.Node, error)
	// PrepareDomainConversion initializes the domain CHECK programmes a
	// conversion from source to target needs, in the statement's phase.
	PrepareDomainConversion(c *Context, source, target *sqltype.Descriptor) error
	// SpecializeRuleParameters replaces a rule action's parameters with the
	// operation's imported values, through the engine's view rewriter.
	SpecializeRuleParameters(c *Context, e expr.Expr) (expr.Expr, bool, error)
	CheckDomain(ctx context.Context, c *Context, target *sqltype.Descriptor, value sqltype.Value) error
	// DomainCatalog is the catalog that c's domain checks read: its captured
	// view, else the statement's domain input or resolution catalog.
	DomainCatalog(c *Context) catalog.Read
	// ReturnsSet reports whether d returns a set, including the installed
	// intagg enumerator and pgrowlocks record source.
	ReturnsSet(d *catalog.Procedure) bool
	// StoredReferenceValid reports whether ref, captured by consumer's stored
	// SQL, still names its object in view, bridging a TEMP owner's epochs.
	StoredReferenceValid(view catalog.Read, consumer *catalog.Relation, ref ids.ObjectRef) bool
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

// AdmitExpression runs the installed execution admission of e.
func AdmitExpression(c *Context, e expr.Expr, view catalog.Read) error {
	return preparation.AdmitExpression(c, e, view)
}

// preflightQuery runs the installed query preflight of node.
func preflightQuery(c *Context, node plan.Node) (plan.Node, error) {
	return preparation.PreflightQuery(c, node)
}

// prepareDomainConversion runs the installed domain programme preparation
// of a source to target conversion.
func prepareDomainConversion(c *Context, source, target *sqltype.Descriptor) error {
	return preparation.PrepareDomainConversion(c, source, target)
}

// specializeRuleParameters runs the installed rule parameter specialization
// of e.
func specializeRuleParameters(c *Context, e expr.Expr) (expr.Expr, bool, error) {
	return preparation.SpecializeRuleParameters(c, e)
}

type missingPreparation struct{}

func (missingPreparation) AdmitExpression(*Context, expr.Expr, catalog.Read) error {
	panic("exec: preparation not installed")
}

func (missingPreparation) PreflightQuery(*Context, plan.Node) (plan.Node, error) {
	panic("exec: preparation not installed")
}

func (missingPreparation) PrepareDomainConversion(*Context, *sqltype.Descriptor, *sqltype.Descriptor) error {
	panic("exec: preparation not installed")
}

func (missingPreparation) SpecializeRuleParameters(*Context, expr.Expr) (expr.Expr, bool, error) {
	panic("exec: preparation not installed")
}

func (missingPreparation) CheckDomain(context.Context, *Context, *sqltype.Descriptor, sqltype.Value) error {
	panic("exec: preparation not installed")
}

func (missingPreparation) DomainCatalog(*Context) catalog.Read {
	panic("exec: preparation not installed")
}

func (missingPreparation) ReturnsSet(*catalog.Procedure) bool {
	panic("exec: preparation not installed")
}

func (missingPreparation) StoredReferenceValid(catalog.Read, *catalog.Relation, ids.ObjectRef) bool {
	panic("exec: preparation not installed")
}

func (missingPreparation) ValidInstalledText(plan.InstalledTextExpr) bool {
	panic("exec: preparation not installed")
}
