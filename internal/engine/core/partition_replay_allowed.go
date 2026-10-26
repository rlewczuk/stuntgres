package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Callers hold Engine.mu, as do the existing hierarchy admission guards.
func PartitionConstraintReplayImport(e *Engine, op *session.Operation, auth AuthorizationView) bool {
	if e == nil || op == nil || op.Self != op || op.Session == nil || EngineOf(op.Session) != e || e.ImportTargets == nil || auth.Owner != &e.Catalog.Owner || auth.Session != op.Session || auth.RoleRecord != e.RoleStore.Published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.Catalog.Domain || auth.Catalog.State() != e.Catalog.State || auth.Catalog.Epoch() != e.Catalog.State.Epoch() {
		return false
	}
	if err := e.validateOperation(op); err != nil {
		return false
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	return err == nil && bootstrap.Superuser
}

func partitionCheckReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, d *catalog.Relation) bool {
	return op != nil && OperationReplayOrZero(op).PartitionCheck == d && partition.CheckReplayTarget(auth.Catalog, d) && partition.CheckReplayActions(raw) && PartitionConstraintReplayImport(operationEngine(op), op, auth)
}

func PartitionUniqueReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) bool {
	if op == nil || raw == nil || OperationReplayOrZero(op).PartitionUnique == nil || !PartitionConstraintReplayImport(operationEngine(op), op, auth) {
		return false
	}
	return OperationReplayOrZero(op).PartitionUnique.Matches(auth.Catalog, raw, table)
}

func partitionUsingIndexReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) bool {
	if op == nil || raw == nil || OperationReplayOrZero(op).PartitionUsingIndex == nil {
		return false
	}
	return OperationReplayOrZero(op).PartitionUsingIndex.Matches(auth.Catalog, raw, table) && PartitionConstraintReplayImport(operationEngine(op), op, auth)
}
