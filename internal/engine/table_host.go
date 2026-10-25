package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
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

// TableHost is the family host of the index, table, partition and FDW
// decisions: it implements table.Host here, index.Host in index_host.go,
// partition.Host in partition_host.go and fdw.Host (fdw_host.go), for CREATE
// TABLE and the staging it shares with PARTITION OF children, ADD IDENTITY,
// FOREIGN KEY and index staging. It also holds the type staging that
// table.Host shares with typedef.StageHost and view.StageHost, which is why
// TypeHost and ViewHost embed it. Like DDLHost, every method runs under
// Engine.mu and never waits.
type TableHost struct {
	DDLHost
}

var _ table.Host = TableHost{}

func (h TableHost) TableStage() *table.Stage { return tableStage(h.CatalogStage) }

func (h TableHost) SequenceStage() *sequence.Stage { return h.CatalogStage.SequenceState() }

// SequenceColumns reads the fixed INT8/BOOL builtin shape, not a sequence
// definition or dependency. Transaction-private definitions use stage authority.
func (h TableHost) SequenceColumns() []catalog.Column {
	return sequence.Columns(h.E.Catalog.Current())
}

func (h TableHost) NameBinding(key ids.NameKey) (ids.ObjectAddress, bool) {
	return h.CatalogStage.NameBinding(key)
}

// StageChanged counts the stage's own entries; StageRead alone would clone
// the state and change the identity of the authority's catalog.
func (h TableHost) StageChanged() bool {
	return h.CatalogStage.DescriptorCount() != 0 || h.CatalogStage.RemovalCount() != 0 || h.CatalogStage.OldNameCount() != 0
}

func (h TableHost) StagedTemporary(a ddl.Authority, namespace *catalog.Namespace) ddl.Authority {
	v := DDLAuthorityOf(a)
	if namespace != nil && !namespace.TemporaryToast && v.Session != nil && namespace.Temporary == v.Session.SessionToken {
		v.StagedTemporary = namespace.Address()
	}
	return v
}

func (h TableHost) ResolveReference(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.E.ResolveReference(DDLAuthorityOf(a), name)
}

func (h TableHost) ResolveRestoredRelation(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.E.ResolveRelationReference(DDLAuthorityOf(a), name, false)
}

func (h TableHost) DescribeCreateTable(a ddl.Authority, s *parser.CreateTable, input sqltype.InputEnvironment) (analyze.Binding, error) {
	return Describe(h.Op.Context(), h.E.AnalysisHost(h.Op), s, DDLAuthorityOf(a), ids.ObjectRef{}, input, nil, nil)
}

func (h TableHost) BindColumnDefaults(a ddl.Authority, columns []catalog.Column, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment, temporary ids.SessionToken) (map[int32]catalog.ColumnDefault, error) {
	v := DDLAuthorityOf(a)
	binder := NewExpressionBinder(h.Op.Context(), h.E, input, h.Op, nil)
	binder.Query.Auth = &v
	binder.Consumer.DefaultTemporary = temporary
	return bindCreateDefaults(binder, columns, declarations)
}

func (h TableHost) BindGenerations(a ddl.Authority, relation *catalog.Relation, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment) error {
	return bindCreateGenerations(h.E, h.Op, DDLAuthorityOf(a), relation, declarations, input)
}

func (h TableHost) BindCheckExpression(a ddl.Authority, relation *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (expr.Expr, []int, error) {
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

func (h TableHost) OperationDefinition() catalog.DefinitionReader { return h.Op.Definition }

func (h TableHost) LiveEpoch() ids.CatalogEpoch { return h.E.Catalog.State.Epoch() }

func (h TableHost) TrustedRestoreDestination(a ddl.Authority, what string) (bool, error) {
	if h.Op == nil || !OperationReplayOrZero(h.Op).TableDestination {
		return false, nil
	}
	auth := DDLAuthorityOf(a)
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || h.E.ImportTargets == nil {
		return true, fmt.Errorf("stuntgres: invalid trusted restore %s authority", what)
	}
	return true, nil
}

func (h TableHost) CreationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	return h.CatalogStage.CreationTypeACL(owner, schema)
}

func (h TableHost) ResolveType(raw parser.TypeName) (sqltype.Usage, error) {
	return h.E.resolveType(raw, h.Op)
}

func (h TableHost) ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error) {
	return h.E.resolveColumnCollation(u, raw, h.Op)
}

func (h TableHost) DisplaceAssociatedArray(namespace ids.ObjectAddress, name string) (bool, error) {
	return h.E.DisplaceAssociatedArrayLocked(h.CatalogStage, namespace, name)
}

func (h TableHost) AssociatedArrayName(namespace ids.ObjectAddress, name string) string {
	return AssociatedArrayName(h.CatalogStage, namespace, name)
}

func (h TableHost) StageRowType(relation *catalog.Relation) error {
	return h.E.StageRowTypeLocked(h.CatalogStage, relation)
}
