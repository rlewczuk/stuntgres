package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
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

func decodeRestoreDomainDefault(r *restoreRun, comment string, position parser.Position) (bool, error) {
	record, recognized, err := typedef.DecodeDomainDefault(comment)
	if err != nil || !recognized {
		return false, err
	}
	r.domainDefaults = append(r.domainDefaults, typedef.PositionedDomainDefault{Record: record, End: position.Offset + len(comment)})
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
	record, recognized, err := view.DecodeMaterializedSnapshot(comment)
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
		return partitioncmds.BeginPartitionCheckReplay(r.stage, op, alteration)
	}
	return nil
}

// prepareRestorePartitionUsingIndex leases the physical target and index of
// an ALTER TABLE ... USING INDEX replay.
func prepareRestorePartitionUsingIndex(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	if alteration, ok := stmt.(*parser.AlterTable); ok {
		return partitioncmds.BeginPartitionUsingIndexReplay(r.stage, op, alteration)
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
	if err := partitioncmds.BeginPartitionUniqueReplay(r.stage, op, alteration); err != nil {
		return err
	}
	if len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.AddUnique && core.OperationReplayOrZero(op).PartitionUnique == nil && r.cur.physicalConstructor == nil {
		return core.SQLError("0A000", "restore accepts only captured private virtual unique definitions")
	}
	return nil
}

// afterRestoreTypeOwner pairs a domain or genuine range created under an owner
// directive with the exact ALTER OWNER that immediately follows it.
func afterRestoreTypeOwner(r *restoreRun, op *session.Operation, stmt parser.Statement, outcome control.Outcome) error {
	if r.cur.ownerForCreate == (authorization.PrincipalRef{}) || outcome.Created == (ids.ObjectRef{}) {
		return nil
	}
	kind := ""
	switch stmt.(type) {
	case *parser.CreateDomain:
		kind = "domain"
	case *parser.CreateRange:
		kind = "type"
	default:
		return nil
	}
	var err error
	r.pairedTypeOwner, err = typecmds.CaptureRestoredTypeOwner(r.stage, op, outcome.Created, r.cur.ownerForCreate, kind)
	return err
}

// finishRestoreExtensionOwners imports the decoded extension owners.
func finishRestoreExtensionOwners(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return importExtensionOwners(r.stage, ctx, op, r.extensionOwners)
}

// finishRestoreMaterializedSnapshots populates materialized views from the
// decoded snapshots.
func finishRestoreMaterializedSnapshots(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return ImportMaterializedSnapshots(r.stage, ctx, op, r.snapshots)
}

// beforeRestoreRoutineDirective consumes the directive that defers SQL
// routine body validation to finish.
func beforeRestoreRoutineDirective(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	if !routine.ReplayDirective(r.source, stmt) {
		return false, nil
	}
	core.OperationReplay(op).SQLBodies = true
	return true, nil
}

// finishRestoreSQLRoutines validates the deferred SQL routine bodies.
func finishRestoreSQLRoutines(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return validateRestoredSQLRoutines(r.stage, ctx, op)
}
