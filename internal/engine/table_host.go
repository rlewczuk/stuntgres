package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ddlHost implements table.Host for CREATE TABLE and the staging it shares
// with PARTITION OF children, ADD IDENTITY and FOREIGN KEY staging. Like the
// rest of ddlHost, every method runs under Engine.mu and never waits.
var _ table.Host = ddlHost{}

func (h ddlHost) TableStage() *table.Stage { return h.stage.table() }

func (h ddlHost) SequenceStage() *sequence.Stage { return h.stage.sequenceState() }

// SequenceColumns types the columns from the live catalog, as identity and
// serial staging always have.
func (h ddlHost) SequenceColumns() []catalog.Column {
	return sequence.Columns(h.e.catalog.read(h.e.catalog.state))
}

func (h ddlHost) NameBinding(key ids.NameKey) (ids.ObjectAddress, bool) {
	return h.stage.NameBinding(key)
}

// StageChanged counts the stage's own entries; StageRead alone would clone
// the state and change the identity of the authority's catalog.
func (h ddlHost) StageChanged() bool {
	return h.stage.DescriptorCount() != 0 || h.stage.RemovalCount() != 0 || h.stage.OldNameCount() != 0
}

func (h ddlHost) StagedTemporary(a ddl.Authority, namespace *catalog.Namespace) ddl.Authority {
	v := ddlAuthorityOf(a)
	if namespace != nil && !namespace.TemporaryToast && v.session != nil && namespace.Temporary == v.session.SessionToken {
		v.stagedTemporary = namespace.Address()
	}
	return v
}

func (h ddlHost) ResolveReference(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.e.resolveReference(ddlAuthorityOf(a), name)
}

func (h ddlHost) ResolveRestoredRelation(a ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.e.resolveRelationReference(ddlAuthorityOf(a), name, false)
}

func (h ddlHost) DescribeCreateTable(a ddl.Authority, s *parser.CreateTable, input sqltype.InputEnvironment) (analyze.Binding, error) {
	return describe(h.op.Context(), h.e.analysisHost(h.op), s, ddlAuthorityOf(a), ids.ObjectRef{}, input, nil, nil)
}

func (h ddlHost) BindColumnDefaults(a ddl.Authority, columns []catalog.Column, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment, temporary ids.SessionToken) (map[int32]catalog.ColumnDefault, error) {
	v := ddlAuthorityOf(a)
	binder := newExpressionBinder(h.op.Context(), h.e, input, h.op, nil)
	binder.Query.Auth = &v
	binder.Consumer.DefaultTemporary = temporary
	return bindCreateDefaults(binder, columns, declarations)
}

func (h ddlHost) BindGenerations(a ddl.Authority, relation *catalog.Relation, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment) error {
	return h.e.bindCreateGenerations(h.op, ddlAuthorityOf(a), relation, declarations, input)
}

func (h ddlHost) BindChecks(a ddl.Authority, relation *catalog.Relation, raw []*parser.CheckConstraint, input sqltype.InputEnvironment, inherited map[string]catalog.CheckConstraint) error {
	return h.e.bindColumnChecks(h.op, ddlAuthorityOf(a), raw, relation, input, false, inherited)
}
