package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// Restore hook entries owned by the type, extension, view, partition and
// routine features. Each calls the feature's existing method.

// decodeRestoreDomainReplay collects a domain replay record and the script
// offset where its comment ends, for pairing with the following INSERT.
func decodeRestoreDomainReplay(r *restoreRun, comment string, position parser.Position) (bool, error) {
	record, recognized, err := typedef.DecodeDomainReplay(comment)
	if err != nil || !recognized {
		return false, err
	}
	r.domainReplays = append(r.domainReplays, typedef.PositionedDomainReplay{Record: record, End: position.Offset + len(comment)})
	return true, nil
}

// decodeRestoreExtensionOwners collects an extension owner record.
func decodeRestoreExtensionOwners(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := extension.DecodeOwners(comment)
	if err != nil || !recognized {
		return false, err
	}
	r.extensionOwners = append(r.extensionOwners, record)
	return true, nil
}

// decodeRestoreMaterializedSnapshot collects a materialized view snapshot.
func decodeRestoreMaterializedSnapshot(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	record, recognized, err := DecodeMaterializedSnapshot(comment)
	if err != nil || !recognized {
		return false, err
	}
	r.snapshots = append(r.snapshots, record)
	return true, nil
}

// prepareRestorePartitionCheck leases the private-import physical CHECK
// target an ALTER TABLE replays into.
func prepareRestorePartitionCheck(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	if alteration, ok := stmt.(*parser.AlterTable); ok {
		return r.stage.beginPartitionCheckReplay(op, alteration)
	}
	return nil
}

// prepareRestorePartitionUsingIndex leases the physical target and index of
// an ALTER TABLE ... USING INDEX replay.
func prepareRestorePartitionUsingIndex(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	if alteration, ok := stmt.(*parser.AlterTable); ok {
		return r.stage.beginPartitionUsingIndexReplay(op, alteration)
	}
	return nil
}

// prepareRestorePartitionUnique leases the private virtual unique
// definition an ADD UNIQUE replays; one without a captured definition or a
// physical constructor is rejected.
func prepareRestorePartitionUnique(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	alteration, ok := stmt.(*parser.AlterTable)
	if !ok {
		return nil
	}
	if err := r.stage.beginPartitionUniqueReplay(op, alteration); err != nil {
		return err
	}
	if len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.AddUnique && operationReplayOrZero(op).partitionUnique == nil && r.cur.physicalConstructor == nil {
		return sqlError("0A000", "restore accepts only captured private virtual unique definitions")
	}
	return nil
}

// afterRestoreDomainOwner pairs a domain created under an owner directive
// with the ALTER DOMAIN OWNER that follows it.
func afterRestoreDomainOwner(r *restoreRun, op *session.Operation, stmt parser.Statement, result Result) error {
	if _, domain := stmt.(*parser.CreateDomain); !domain || r.cur.ownerForCreate == (authorization.PrincipalRef{}) || result.created == (ids.ObjectRef{}) {
		return nil
	}
	var err error
	r.pairedDomainOwner, err = r.stage.captureRestoredDomainOwner(op, result.created, r.cur.ownerForCreate)
	return err
}

// finishRestoreExtensionOwners imports the decoded extension owners.
func finishRestoreExtensionOwners(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return r.stage.importExtensionOwners(ctx, op, r.extensionOwners)
}

// finishRestoreMaterializedSnapshots populates materialized views from the
// decoded snapshots.
func finishRestoreMaterializedSnapshots(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return r.stage.importMaterializedSnapshots(ctx, op, r.snapshots)
}

// beforeRestoreRoutineDirective consumes the directive that defers SQL
// routine body validation to finish.
func beforeRestoreRoutineDirective(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	if !routine.ReplayDirective(r.source, stmt) {
		return false, nil
	}
	operationReplay(op).sqlBodies = true
	return true, nil
}

// finishRestoreSQLRoutines validates the deferred SQL routine bodies.
func finishRestoreSQLRoutines(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return r.stage.validateRestoredSQLRoutines(ctx, op)
}
