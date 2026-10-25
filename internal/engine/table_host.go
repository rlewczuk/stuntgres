package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ddlHost implements table.Host for CREATE TABLE and the staging it shares
// with PARTITION OF children, ADD IDENTITY and FOREIGN KEY staging. Like the
// rest of ddlHost, every method runs under Engine.mu and never waits.
var _ table.Host = DDLHost{}

func (h DDLHost) TableStage() *table.Stage { return h.CatalogStage.table() }

func (h DDLHost) SequenceStage() *sequence.Stage { return h.CatalogStage.SequenceState() }

// SequenceColumns reads the fixed INT8/BOOL builtin shape, not a sequence
// definition or dependency. Transaction-private definitions use stage authority.
func (h DDLHost) SequenceColumns() []catalog.Column {
	return sequence.Columns(h.E.Catalog.Current())
}

func (h DDLHost) NameBinding(key ids.NameKey) (ids.ObjectAddress, bool) {
	return h.CatalogStage.NameBinding(key)
}

// StageChanged counts the stage's own entries; StageRead alone would clone
// the state and change the identity of the authority's catalog.
func (h DDLHost) StageChanged() bool {
	return h.CatalogStage.DescriptorCount() != 0 || h.CatalogStage.RemovalCount() != 0 || h.CatalogStage.OldNameCount() != 0
}

func (h DDLHost) StagedTemporary(a ddl.Authority, namespace *catalog.Namespace) ddl.Authority {
	v := DDLAuthorityOf(a)
	if namespace != nil && !namespace.TemporaryToast && v.Session != nil && namespace.Temporary == v.Session.SessionToken {
		v.StagedTemporary = namespace.Address()
	}
	return v
}

func (h DDLHost) ResolveReference(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.E.ResolveReference(DDLAuthorityOf(a), name)
}

func (h DDLHost) ResolveRestoredRelation(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.E.ResolveRelationReference(DDLAuthorityOf(a), name, false)
}

func (h DDLHost) DescribeCreateTable(a ddl.Authority, s *parser.CreateTable, input sqltype.InputEnvironment) (analyze.Binding, error) {
	return Describe(h.Op.Context(), h.E.AnalysisHost(h.Op), s, DDLAuthorityOf(a), ids.ObjectRef{}, input, nil, nil)
}

func (h DDLHost) BindColumnDefaults(a ddl.Authority, columns []catalog.Column, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment, temporary ids.SessionToken) (map[int32]catalog.ColumnDefault, error) {
	v := DDLAuthorityOf(a)
	binder := NewExpressionBinder(h.Op.Context(), h.E, input, h.Op, nil)
	binder.Query.Auth = &v
	binder.Consumer.DefaultTemporary = temporary
	return bindCreateDefaults(binder, columns, declarations)
}

func (h DDLHost) BindGenerations(a ddl.Authority, relation *catalog.Relation, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment) error {
	return h.E.bindCreateGenerations(h.Op, DDLAuthorityOf(a), relation, declarations, input)
}

func (h DDLHost) BindCheckExpression(a ddl.Authority, relation *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (expr.Expr, []int, error) {
	auth := DDLAuthorityOf(a)
	ref := h.E.Catalog.Domain.Ref(auth.Catalog.Epoch(), relation.Address())
	namespace := analyze.RelationNamespace(auth.Catalog, relation, ref, 0)
	binder := NewExpressionBinder(h.Op.Context(), h.E, input, h.Op, nil)
	binder.Consumer.CheckConsumer, binder.Params.NoParameters = true, true
	binder.Query.Auth = &auth
	binder.Query.Namespace = &namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: relation}}
	node, err := binder.BindBoolean(raw, analyze.ProjectionContext, "CHECK")
	if err != nil {
		return nil, nil, err
	}
	return node, binder.Query.Sources[0].Uses, nil
}

func (h DDLHost) OperationDefinition() catalog.DefinitionReader { return h.Op.Definition }

func (h DDLHost) LiveEpoch() ids.CatalogEpoch { return h.E.Catalog.State.Epoch() }
