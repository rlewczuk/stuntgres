package tablecmds

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
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

// AlterHost implements table.AlterHost: TableHost bound to one statement
// scope, whose input, output and inquiry state the ALTER bindings and captures
// read and write. Callers build one per call because the ALTER
// loop begins its stage lazily, so stage may be nil. Every method runs under
// Engine.mu and never waits.
type AlterHost struct {
	TableHost
	Scope *core.StatementScope
}

var _ table.AlterHost = AlterHost{}

func (h AlterHost) Input() sqltype.InputEnvironment { return h.Scope.Input }

func (h AlterHost) CaptureTypeDiagnosticName(a ddl.Authority) error {
	name, err := core.CaptureTypeDiagnosticName(core.DDLAuthorityOf(a))
	if err != nil {
		return err
	}
	h.Scope.Input.TypeDiagnosticName = name
	return nil
}

func (h AlterHost) CaptureCommentSnapshot() error { return h.E.CaptureCommentSnapshotLocked(h.Scope) }

func (h AlterHost) CaptureCheckSnapshots(relation *catalog.Relation, visibility bool) error {
	if err := h.E.CaptureCheckCommentSnapshotLocked(h.Scope, relation); err != nil || !visibility {
		return err
	}
	return h.E.CaptureCheckVisibilitySnapshotLocked(h.Scope, relation)
}

func (h AlterHost) BindDefault(a ddl.Authority, relation *catalog.Relation, column catalog.Column, raw parser.Expr) (expr.Expr, error) {
	auth := core.DDLAuthorityOf(a)
	binder := core.NewExpressionBinder(h.Op.Context(), h.E, h.Scope.Input, h.Op, nil)
	binder.Query.Auth = &auth
	binder.Consumer.DefaultTemporary = auth.Catalog.MustNamespace(relation.Namespace()).Temporary
	return binder.BindDefault(raw, column)
}

func (h AlterHost) BindGeneration(a ddl.Authority, relation *catalog.Relation, slot int, raw parser.Expr) (catalog.ColumnGeneration, error) {
	auth := core.DDLAuthorityOf(a)
	namespace := GenerationNamespace(h.E, auth, relation)
	return BindGeneration(h.E, h.Op, auth, relation, slot, raw, h.Scope.Input, &namespace)
}

func (h AlterHost) RebindCheck(a ddl.Authority, relation *catalog.Relation, ref ids.ObjectRef, raw parser.Expr, literals []plan.LiteralExpr) (expr.Expr, error) {
	auth := core.DDLAuthorityOf(a)
	namespace := analyze.RelationNamespace(auth.Catalog, relation, ref, 0)
	binder := core.NewExpressionBinder(h.Op.Context(), h.E, h.Scope.Input, h.Op, nil)
	binder.Consumer.CheckConsumer, binder.Params.NoParameters, binder.Query.Auth = true, true, &auth
	binder.Stored.CheckLiterals = literals
	binder.Query.Namespace = &namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: relation}}
	return binder.BindBoolean(raw, analyze.ProjectionContext, "CHECK")
}

func (h AlterHost) BindAlterTypeUsing(a ddl.Authority, original *catalog.Relation, ref ids.ObjectRef, using parser.Expr, usage sqltype.Usage) (expr.Expr, []catalog.ExpressionDependency, error) {
	auth := core.DDLAuthorityOf(a)
	binder := core.NewExpressionBinder(h.Scope.Op.Context(), h.E, h.Scope.Input, h.Scope.Op, nil)
	binder.Target.Relation, binder.Target.Ref, binder.Params.NoParameters, binder.Query.Auth = original, ref, true, &auth
	namespace := analyze.RelationNamespace(auth.Catalog, original, ref, analyze.QueryTargetLeaf)
	binder.Query.Namespace = &namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: original}}
	node, err := binder.Bind(using, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: usage.Type.Usage()})
	if err == nil && (binder.Prepared.RegclassConcat || binder.Consumer.CatalogSnapshot) {
		err = h.E.CaptureCommentSnapshotLocked(h.Scope)
	}
	return node, binder.Dependencies, err
}

// TypeChangeDependents reads the stage's dependent views when one is begun;
// before that, the authority's catalog alone.
func (h AlterHost) TypeChangeDependents(a ddl.Authority, target ids.ObjectAddress, column string) error {
	auth := core.DDLAuthorityOf(a)
	if err := view.PolicyTypeChangeError(auth.Catalog, target); err != nil {
		return err
	}
	path, err := core.LookupPath(auth, analyze.RelationLookup)
	if err != nil {
		return err
	}
	if err := view.PublicationTypeChangeError(auth.Catalog, target, column, path); err != nil {
		return err
	}
	return view.TypeChangeError(&core.DropProvider{Base: auth.Catalog, SearchPath: path, Stage: h.CatalogStage}, target, column)
}

func (h AlterHost) StageIdentitySequenceOptions(relation *catalog.Relation, attribute int32, sequence ids.ObjectAddress, raw []parser.SequenceOption) error {
	return stageIdentitySequenceOptionsLocked(h.E, h.Scope, h.CatalogStage, relation, attribute, sequence, raw)
}
