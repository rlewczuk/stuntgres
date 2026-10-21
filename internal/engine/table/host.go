package table

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Host is what CREATE TABLE, the CREATE SCHEMA table element, PARTITION OF
// children, LIKE, identity and serial columns and FOREIGN KEY staging need
// from the engine for one statement: the index host (and through it the
// shared DDL host), type staging, the table and sequence stage slots, the
// temporary-namespace authority, relation resolution and expression
// binding. The engine builds binders; this package never does.
//
// Every method runs with Engine.mu held, never takes or releases it, and
// never waits.
type Host interface {
	index.Host
	ddl.TypeStaging
	Resolver
	RestoreResolver

	// StageRowType stages the composite row type of relation.
	StageRowType(relation *catalog.Relation) error
	// ResolveType resolves a column type name with the operation's authority.
	ResolveType(raw parser.TypeName) (sqltype.Usage, error)
	// ResolveColumnCollation applies a COLLATE clause to a column type.
	ResolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error)

	// TableStage is the stage's table slot, allocated on first use.
	TableStage() *Stage
	// SequenceStage is the stage's sequence slot, allocated on first use.
	SequenceStage() *sequence.Stage
	// SequenceColumns are the columns of a new sequence relation, typed from
	// the live catalog, not the stage.
	SequenceColumns() []catalog.Column
	// NameBinding reports the stage's own binding of key, ignoring the base.
	NameBinding(key ids.NameKey) (ids.ObjectAddress, bool)
	// StageChanged reports whether the stage holds any descriptor, removal
	// or vacated name.
	StageChanged() bool

	// StagedTemporary returns auth seeing namespace as its session's
	// temporary namespace before publication, when namespace is the
	// session's own non-TOAST temporary namespace; otherwise auth unchanged.
	StagedTemporary(auth ddl.Authority, namespace *catalog.Namespace) ddl.Authority

	// DescribeCreateTable analyzes s.
	DescribeCreateTable(auth ddl.Authority, s *parser.CreateTable, input sqltype.InputEnvironment) (analyze.Binding, error)
	// BindColumnDefaults binds the DEFAULT expressions of declarations, keyed
	// by attribute; temporary is the session token of a temporary table's
	// namespace, zero otherwise.
	BindColumnDefaults(auth ddl.Authority, columns []catalog.Column, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment, temporary ids.SessionToken) (map[int32]catalog.ColumnDefault, error)
	// BindGenerations binds the generation expressions of declarations into
	// relation, sharing one column namespace.
	BindGenerations(auth ddl.Authority, relation *catalog.Relation, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment) error
	// BindChecks binds raw CHECK constraints into relation; inherited holds
	// the parents' checks to merge with.
	BindChecks(auth ddl.Authority, relation *catalog.Relation, raw []*parser.CheckConstraint, input sqltype.InputEnvironment, inherited map[string]catalog.CheckConstraint) error
}

// Resolver is what resolving relation names needs: the authority's view
// and the analyzed-name resolution, with a positioned 42P01 or 42501. It
// reads no stage, so DROP TABLE resolves its targets before it begins one.
// Its methods run with Engine.mu held and never wait.
type Resolver interface {
	AccessView(auth ddl.Authority) access.View
	ResolveReference(auth ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error)
}

// AlterHost is what the ALTER TABLE decisions need beyond Host: the
// statement's live input environment, the snapshot captures that mutate it,
// and the bindings of one ALTER action. It is bound to one statement scope.
// Table 24d.2 implements it; every method runs with Engine.mu held, never
// takes or releases it, and never waits.
type AlterHost interface {
	Host

	// Input is the statement's live input environment. Captures mutate it, so
	// callers read it again before every binding call.
	Input() sqltype.InputEnvironment
	// CaptureTypeDiagnosticName records the type diagnostic names of auth.
	CaptureTypeDiagnosticName(auth ddl.Authority) error
	// CaptureCommentSnapshot records the comment snapshot.
	CaptureCommentSnapshot() error
	// CaptureCheckSnapshots records the CHECK comment snapshot of relation,
	// and its visibility snapshot when visibility is set.
	CaptureCheckSnapshots(relation *catalog.Relation, visibility bool) error

	// CookDefault binds and renders one DEFAULT of relation's slot;
	// withDefinition renders it with the operation's definition reader.
	CookDefault(auth ddl.Authority, relation *catalog.Relation, slot int, raw parser.Expr, withDefinition bool) (expr.Expr, catalog.ColumnDefault, error)
	// BindGeneration binds one generation expression of relation's slot.
	BindGeneration(auth ddl.Authority, relation *catalog.Relation, slot int, raw parser.Expr) (catalog.ColumnGeneration, error)
	// RebindCheck binds a CHECK expression of relation again.
	RebindCheck(auth ddl.Authority, relation *catalog.Relation, ref ids.ObjectRef, raw parser.Expr, literals []plan.LiteralExpr) (expr.Expr, error)
	// BindAlterTypeUsing binds the USING expression of ALTER COLUMN TYPE.
	BindAlterTypeUsing(auth ddl.Authority, original *catalog.Relation, ref ids.ObjectRef, column string, using parser.Expr, usage sqltype.Usage) (expr.Expr, []catalog.ExpressionDependency, error)
	// OperationDefinition reads definitions as the operation sees them.
	OperationDefinition() catalog.DefinitionReader

	// TypeChangeDependents rejects ALTER COLUMN TYPE on a column that a
	// policy, rule or view depends on.
	TypeChangeDependents(auth ddl.Authority, target ids.ObjectAddress) error
	// StageIdentitySequenceOptions stages the sequence options of an
	// identity column; the caller holds the sequence's grant.
	StageIdentitySequenceOptions(relation *catalog.Relation, attribute int32, sequence ids.ObjectAddress, raw []parser.SequenceOption) error
}

// RestoreResolver resolves the relation names of Restore repairs. Its method
// runs with Engine.mu held and never waits.
type RestoreResolver interface {
	// ResolveRestoredRelation resolves a relation name of a Restore repair.
	ResolveRestoredRelation(auth ddl.Authority, name parser.ObjectName) (ids.ObjectRef, error)
}
