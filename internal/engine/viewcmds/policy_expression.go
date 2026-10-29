package viewcmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Engine.mu is held. Analysis shares the ordinary Boolean binder and frozen
// query presentation; no condition is evaluated and no row storage is opened.
func (h ViewHost) BindPolicyExpression(a ddl.Authority, d *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (*stored.Policy, error) {
	return core.BindRetainedPredicate(h.E, h.Op, core.DDLAuthorityOf(a), d, raw, input, "POLICY", "policy expressions")
}
