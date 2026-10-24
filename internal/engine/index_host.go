package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ddlHost implements index.Host for CREATE INDEX, ALTER/DROP INDEX, REINDEX
// and constraint-index staging. Like the rest of ddlHost, every method runs
// under Engine.mu and never waits.
var _ index.Host = ddlHost{}

func (h ddlHost) IndexStage() *index.Stage { return h.stage.indexes() }

func (h ddlHost) DescriptorStage() index.DescriptorStage { return h.stage }

func (h ddlHost) ACLOutput(view catalog.Read) regobj.ACLItemOutput {
	owner := &h.e.catalog.Owner
	return regobj.ACLItemOutput{Owner: owner, Roles: h.e.roles.Read(owner, view, h.e.operationRolesLocked(h.op))}
}

func (h ddlHost) DefinitionReader() catalog.DefinitionReader { return stageDefinitions(h.stage) }

func (h ddlHost) ValidityTarget() index.ValidityTarget {
	return stageDropProvider(context.Background(), h.stage, nil, nil, nil)
}

func (h ddlHost) LookupCollation(raw parser.ObjectName) (uint32, error) {
	return h.e.lookupCollation(raw, h.op)
}

func (h ddlHost) BindIndexExpression(a ddl.Authority, parent *catalog.Relation, raw parser.Expr, literals []plan.LiteralExpr, input sqltype.InputEnvironment) (*catalog.DerivedIndexKey, string, error) {
	return h.e.bindIndexExpression(h.op, input, ddlAuthorityOf(a), parent, raw, literals)
}

func (h ddlHost) BindIndexPredicate(a ddl.Authority, parent *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (*catalog.IndexPredicate, error) {
	return h.e.bindIndexPredicate(h.op, input, ddlAuthorityOf(a), parent, raw)
}

var _ index.DropStage = (*CatalogStage)(nil)

// StageRead reads the stage over its base.
func (s *CatalogStage) StageRead() catalog.Read { return s.owner.stageRead(s) }

// RemoveRelation stages the removal of the relation at address. Like Remove,
// it takes Engine.mu; the caller holds nothing.
func (s *CatalogStage) RemoveRelation(address ids.ObjectAddress) error {
	return s.Remove(s.owner.catalog.Domain.Ref(s.Base.Epoch(), address))
}

var _ index.ValidityTarget = (*dropProvider)(nil)
