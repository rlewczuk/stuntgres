package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// TemporaryNamespace calls temporaryNamespace.
func TemporaryNamespace(auth AuthorizationView) ids.ObjectAddress {
	return temporaryNamespace(auth)
}

// TemporarySessionIndexes names temporarySessionIndexes.
type TemporarySessionIndexes = temporarySessionIndexes

// EngineEndTemporarySession calls x.endTemporarySession.
func EngineEndTemporarySession(x *Engine, s *session.Session) error {
	return x.endTemporarySession(s)
}

// EngineInstallTemporarySessionStageLocked calls x.installTemporarySessionStageLocked.
func EngineInstallTemporarySessionStageLocked(x *Engine, s *session.Session, stage *CatalogStage, namespaces []ids.ObjectAddress, indexes *temporarySessionIndexes) (bool, error) {
	return x.installTemporarySessionStageLocked(s, stage, namespaces, indexes)
}

// EnginePrepareTemporaryCommit calls x.prepareTemporaryCommit.
func EnginePrepareTemporaryCommit(x *Engine, op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal) (map[ids.ObjectAddress][]storage.RowVersion, map[ids.ObjectAddress]*visibility.PrivateDeletes, *session.Journal, error) {
	return x.prepareTemporaryCommit(op, ref, writes, deletes, c, nil)
}

// EnginePrepareTemporarySessionIndexes calls x.prepareTemporarySessionIndexes.
func EnginePrepareTemporarySessionIndexes(x *Engine, stage *CatalogStage) (*temporarySessionIndexes, error) {
	return x.prepareTemporarySessionIndexes(stage)
}

// EnginePreserveTemporaryLocked calls x.preserveTemporaryLocked.
func EnginePreserveTemporaryLocked(x *Engine, ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, restoreOp *session.Operation, indexes *index.Snapshot, principal catalog.ProcedureRemapper, oids *catalog.OIDReservation) error {
	return x.preserveTemporaryLocked(ctx, next, relations, sequences, restoreOp, indexes, principal, oids)
}

// TemporarySessionIndexesPrevious returns x.previous.
func TemporarySessionIndexesPrevious(x *temporarySessionIndexes) map[ids.ObjectAddress]*storage.IndexRoot {
	return x.previous
}

// TemporarySessionIndexesRoots returns x.roots.
func TemporarySessionIndexesRoots(x *temporarySessionIndexes) map[ids.ObjectAddress]*storage.IndexRoot {
	return x.roots
}
