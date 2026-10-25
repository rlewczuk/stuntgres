package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TableHost implements index.Host for CREATE INDEX, ALTER/DROP INDEX, REINDEX
// and constraint-index staging; table.Host embeds it. Like DDLHost, every
// method runs under Engine.mu and never waits.
var _ index.Host = TableHost{}

func (h TableHost) IndexStage() *index.Stage { return h.CatalogStage.Indexes() }

func (h TableHost) DescriptorStage() index.DescriptorStage { return h.CatalogStage }

func (h TableHost) ACLOutput(view catalog.Read) regobj.ACLItemOutput {
	owner := &h.E.Catalog.Owner
	return regobj.ACLItemOutput{Owner: owner, Roles: h.E.RoleStore.Read(owner, view, h.E.OperationRolesLocked(h.Op))}
}

func (h TableHost) DefinitionReader() catalog.DefinitionReader {
	return core.StageDefinitions(h.CatalogStage)
}

func (h TableHost) ValidityTarget() index.ValidityTarget {
	return core.StageDropProvider(context.Background(), h.CatalogStage, nil, nil, nil)
}

func (h TableHost) BindIndexExpression(a ddl.Authority, parent *catalog.Relation, raw parser.Expr, literals []plan.LiteralExpr, input sqltype.InputEnvironment) (*catalog.DerivedIndexKey, string, error) {
	return bindIndexExpression(h.E, h.Op, input, core.DDLAuthorityOf(a), parent, raw, literals)
}

func (h TableHost) BindIndexPredicate(a ddl.Authority, parent *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (*catalog.IndexPredicate, error) {
	return bindIndexPredicate(h.E, h.Op, input, core.DDLAuthorityOf(a), parent, raw)
}
