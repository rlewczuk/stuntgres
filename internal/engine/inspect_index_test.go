package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// EngineCaptureIndexSnapshot calls x.captureIndexSnapshot.
func EngineCaptureIndexSnapshot(x *Engine, ctx context.Context, catalog catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq) (*index.Snapshot, error) {
	return x.captureIndexSnapshot(ctx, catalog, relations, roots, frontier)
}

// EnginePrepareForeignIndexStorage calls x.prepareForeignIndexStorage.
func EnginePrepareForeignIndexStorage(x *Engine, ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, snapshot *index.Snapshot) (index.RemintedIndexStorage, error) {
	return x.prepareForeignIndexStorage(ctx, next, relations, snapshot)
}

// EnginePrepareIndexCatalog calls x.prepareIndexCatalog.
func EnginePrepareIndexCatalog(x *Engine, evaluation *exec.Context, current *catalog.State, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]index.RootCandidate) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	return x.prepareIndexCatalog(evaluation, current, next, relations, roots, rebuildParents, rebuildIndexes, output, auth, runtimeOwners, candidates)
}

// EnginePrepareIndexRoots calls x.prepareIndexRoots.
func EnginePrepareIndexRoots(x *Engine, evaluation *exec.Context, current *catalog.State, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, publications []index.RowPublication, materialize bool, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]index.RootCandidate) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	return x.prepareIndexRoots(evaluation, current, next, relations, roots, rebuildParents, rebuildIndexes, publications, materialize, output, auth, runtimeOwners, candidates)
}

// BindIndexExpression calls bindIndexExpression.
func BindIndexExpression(e *Engine, op *session.Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation, raw parser.Expr, literals []plan.LiteralExpr) (*catalog.DerivedIndexKey, string, error) {
	return bindIndexExpression(e, op, input, auth, parent, raw, literals)
}

// EngineCapturePartialMaskLocked calls x.capturePartialMaskLocked.
func EngineCapturePartialMaskLocked(x *Engine, scope *StatementScope, leaf exec.Leaf, view *visibility.View, mark plan.RowMark) *storage.IndexRoot {
	return x.capturePartialMaskLocked(scope, leaf, view, mark)
}

// EnginePrepareRemintedStorage calls x.prepareRemintedStorage.
func EnginePrepareRemintedStorage(x *Engine, ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, snapshot *index.Snapshot, preserveProvenance bool) (map[ids.ObjectAddress]*storage.IndexRoot, uint64, uint64, error) {
	return x.prepareRemintedStorage(ctx, next, relations, snapshot, preserveProvenance)
}

// ScopeValidatePrivateIndexExpressions calls x.validatePrivateIndexExpressions.
func ScopeValidatePrivateIndexExpressions(x *StatementScope, changes *session.Journal, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) error {
	return x.validatePrivateIndexExpressions(changes, deletes)
}
