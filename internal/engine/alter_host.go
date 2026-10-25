package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// alterHost implements table.AlterHost: TableHost bound to one statement
// scope, whose input, output and inquiry state the ALTER bindings and captures
// read and write. Callers build one per call because the ALTER
// loop begins its stage lazily, so stage may be nil. Every method runs under
// Engine.mu and never waits.
type alterHost struct {
	TableHost
	scope *StatementScope
}

var _ table.AlterHost = alterHost{}

func (h alterHost) Input() sqltype.InputEnvironment { return h.scope.Input }

func (h alterHost) CaptureTypeDiagnosticName(a ddl.Authority) error {
	name, err := CaptureTypeDiagnosticName(DDLAuthorityOf(a))
	if err != nil {
		return err
	}
	h.scope.Input.TypeDiagnosticName = name
	return nil
}

func (h alterHost) CaptureCommentSnapshot() error { return h.E.CaptureCommentSnapshotLocked(h.scope) }

func (h alterHost) CaptureCheckSnapshots(relation *catalog.Relation, visibility bool) error {
	if err := h.E.CaptureCheckCommentSnapshotLocked(h.scope, relation); err != nil || !visibility {
		return err
	}
	return h.E.CaptureCheckVisibilitySnapshotLocked(h.scope, relation)
}

func (h alterHost) BindDefault(a ddl.Authority, relation *catalog.Relation, column catalog.Column, raw parser.Expr) (expr.Expr, error) {
	auth := DDLAuthorityOf(a)
	binder := NewExpressionBinder(h.Op.Context(), h.E, h.scope.Input, h.Op, nil)
	binder.Query.Auth = &auth
	binder.Consumer.DefaultTemporary = auth.Catalog.MustNamespace(relation.Namespace()).Temporary
	return binder.BindDefault(raw, column)
}

func (h alterHost) BindGeneration(a ddl.Authority, relation *catalog.Relation, slot int, raw parser.Expr) (catalog.ColumnGeneration, error) {
	auth := DDLAuthorityOf(a)
	namespace := h.E.GenerationNamespace(auth, relation)
	return h.E.BindGeneration(h.Op, auth, relation, slot, raw, h.scope.Input, &namespace)
}

func (h alterHost) RebindCheck(a ddl.Authority, relation *catalog.Relation, ref ids.ObjectRef, raw parser.Expr, literals []plan.LiteralExpr) (expr.Expr, error) {
	auth := DDLAuthorityOf(a)
	namespace := analyze.RelationNamespace(auth.Catalog, relation, ref, 0)
	binder := NewExpressionBinder(h.Op.Context(), h.E, h.scope.Input, h.Op, nil)
	binder.Consumer.CheckConsumer, binder.Params.NoParameters, binder.Query.Auth = true, true, &auth
	binder.Stored.CheckLiterals = literals
	binder.Query.Namespace = &namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: relation}}
	return binder.BindBoolean(raw, analyze.ProjectionContext, "CHECK")
}

func (h alterHost) BindAlterTypeUsing(a ddl.Authority, original *catalog.Relation, ref ids.ObjectRef, using parser.Expr, usage sqltype.Usage) (expr.Expr, []catalog.ExpressionDependency, error) {
	auth := DDLAuthorityOf(a)
	binder := NewExpressionBinder(h.scope.Op.Context(), h.E, h.scope.Input, h.scope.Op, nil)
	binder.Target.Relation, binder.Target.Ref, binder.Params.NoParameters, binder.Query.Auth = original, ref, true, &auth
	namespace := analyze.RelationNamespace(auth.Catalog, original, ref, analyze.QueryTargetLeaf)
	binder.Query.Namespace = &namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: original}}
	node, err := binder.Bind(using, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: usage.Type.Usage()})
	if err == nil && (binder.Prepared.RegclassConcat || binder.Consumer.CatalogSnapshot) {
		err = h.E.CaptureCommentSnapshotLocked(h.scope)
	}
	return node, binder.Dependencies, err
}

// TypeChangeDependents reads the stage's dependent views when one is begun;
// before that, the authority's catalog alone.
func (h alterHost) TypeChangeDependents(a ddl.Authority, target ids.ObjectAddress, column string) error {
	auth := DDLAuthorityOf(a)
	if err := view.PolicyTypeChangeError(auth.Catalog, target); err != nil {
		return err
	}
	path, err := LookupPath(auth, analyze.RelationLookup)
	if err != nil {
		return err
	}
	return view.TypeChangeError(&DropProvider{Base: auth.Catalog, SearchPath: path, Stage: h.CatalogStage}, target, column)
}

func (h alterHost) StageIdentitySequenceOptions(relation *catalog.Relation, attribute int32, sequence ids.ObjectAddress, raw []parser.SequenceOption) error {
	return h.E.stageIdentitySequenceOptionsLocked(h.scope, h.CatalogStage, relation, attribute, sequence, raw)
}
