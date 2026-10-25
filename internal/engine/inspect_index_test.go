package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
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
