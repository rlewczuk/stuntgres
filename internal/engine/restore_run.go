package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// restoreRun is the state of one native Restore: the parsed script, the
// records the comment decoders collected, and the captures carried from one
// statement to the next. It lives for one Restore call and only the goroutine
// running that call touches it. Every step runs against stage, the private
// restore engine, never against the target engine.
type restoreRun struct {
	stage      *Engine
	source     string
	statements []parser.Statement

	// Records decoded from the script's line comments.
	domainReplays     []typedef.PositionedDomainReplay
	domainReplayPairs map[*parser.Insert]typedef.DomainReplayRecord
	extensionOwners   []extension.OwnerRecord
	snapshots         []view.MaterializedSnapshot

	// Table, NOT NULL and foreign key captures.
	pendingForeignKeys      []table.RestoredForeignKeys
	notNullOrigins          map[ids.OID]table.RestoredNotNull
	pairedNotNull           table.RestoredNotNull
	pairedPhysical          *table.RestoredPhysicalNotNull
	deferredPhysical        *table.RestoredPhysicalNotNull
	physicalIndexes         map[ids.ObjectAddress]*catalog.Relation
	physicalForeignProvider *table.RestoredPhysicalForeignKeyProvider

	// Inheritance captures.
	placeholderOrigins  map[table.RestoredGenerationKey]table.RestoredGenerationRecipe
	generationRepairs   []table.RestoredGenerationRecipe
	historicalRefs      map[ids.ObjectAddress]ids.ObjectRef
	historicalAuthority *table.RestoredHistoryAuthority

	// Partition captures.
	partitionTeardowns map[ids.ObjectAddress]partition.ReplayTeardown

	// Owner directives: the last owner directive's principal, the owner a
	// following CREATE inherits, and the owner pairs a following ALTER OWNER
	// consumes.
	lastAuthorization, createOwner authorization.PrincipalRef
	pairedOwner                    table.RestoredInheritedOwner
	pairedDomainOwner              typedef.RestoredDomainOwner

	cur restoreStatement
}

// restoreStatement is the state of the statement being replayed. Restore
// resets it before each statement.
type restoreStatement struct {
	index               int
	physicalConstructor *table.PhysicalNotNullConstructor
	physicalIndexBase   catalog.Read
	pending             table.RestoredForeignKeys // CREATE TABLE foreign keys bound at finish
	// Owner directive state as it was before this statement.
	priorAuthorization, ownerForCreate authorization.PrincipalRef
	ownerPair                          table.RestoredInheritedOwner
	domainOwnerPair                    typedef.RestoredDomainOwner
}

// startCaptures allocates the capture maps once the restore session is
// running. archiveHighWater is the highest retired-history archive number
// already in the target catalog.
func (r *restoreRun) startCaptures(archiveHighWater uint64) {
	r.partitionTeardowns = make(map[ids.ObjectAddress]partition.ReplayTeardown)
	r.notNullOrigins = make(map[ids.OID]table.RestoredNotNull)
	r.physicalIndexes = make(map[ids.ObjectAddress]*catalog.Relation)
	r.placeholderOrigins = make(map[table.RestoredGenerationKey]table.RestoredGenerationRecipe)
	r.historicalRefs = make(map[ids.ObjectAddress]ids.ObjectRef)
	r.historicalAuthority = &table.RestoredHistoryAuthority{Facts: make(map[ids.OID]table.RestoredHistoricalFact), Archives: make(map[uint64]uint64), Sources: make(map[table.RetiredSourceIdentity]table.RetiredSourceDescription), NextArchive: archiveHighWater}
}

// restoreHost is the root's side of one Restore. Only Start and Publish touch
// target, the engine being restored; every hook entry runs on run.stage.
type restoreHost struct {
	target *Engine
	run    *restoreRun
}

// Begin records the parsed script on the run and pairs the decoded domain
// replay records with their INSERT statements.
func (h *restoreHost) Begin(source string, statements []parser.Statement) error {
	r := h.run
	r.source, r.statements = source, statements
	var err error
	r.domainReplayPairs, err = typedef.PairDomainReplay(r.source, r.statements, r.domainReplays)
	return err
}

// Start starts the stage session as the bootstrap superuser, acquires its
// operation and allocates the captures. end clears the replay flags, releases
// the operation and ends the session, in that order.
func (h *restoreHost) Start(ctx context.Context) (*session.Operation, func(), error) {
	stage := h.run.stage
	// The isolated role snapshot retains exact target identities for final remapping.
	sess, err := stage.StartSession(ctx, session.SessionStart{ID: 1, Authenticated: stage.roles.domain.Principal(10)})
	if err != nil {
		return nil, nil, err
	}
	endSession := func() { sess.RequestEnd(session.CleanClose); _ = sess.WaitEnd(context.Background()) }
	op, err := sess.Acquire(ctx)
	if err != nil {
		endSession()
		return nil, nil, err
	}
	end := func() {
		operationReplay(op).sqlBodies = false
		operationReplay(op).defaultReplay = false
		operationReplay(op).largeObjectAuthority = false
		operationReplay(op).partitionAttach = false
		operationReplay(op).partitionCheck = nil
		operationReplay(op).partitionUsingIndex = nil
		operationReplay(op).partitionUnique = nil
		op.Done()
		endSession()
	}
	h.run.startCaptures(table.RetiredArchiveHighWater(h.target.CatalogRead()))
	return op, end, nil
}

// Statement resets the per-statement state before statement index.
func (h *restoreHost) Statement(index int) {
	h.run.cur = restoreStatement{index: index}
}

// Execute runs one admitted statement on the stage.
func (h *restoreHost) Execute(op *session.Operation, stmt parser.Statement) (control.Outcome, error) {
	return h.run.execute(op, stmt)
}

// Publish installs the stage catalog into the target atomically.
func (h *restoreHost) Publish(ctx context.Context, op *session.Operation) error {
	_, err := h.target.replaceCatalog(ctx, h.run.stage, h.run.stage.CatalogRead(), op)
	return err
}

// execute runs one admitted statement on the stage, then clears the
// per-statement replay flags, closes any rows and detaches the notices.
func (r *restoreRun) execute(op *session.Operation, stmt parser.Statement) (control.Outcome, error) {
	var err error
	var result Result
	if insertion, ok := stmt.(*parser.Insert); ok {
		if record, paired := r.domainReplayPairs[insertion]; paired {
			result, err = r.stage.executeDomainReplayInsert(op, insertion, record)
		} else {
			result, err = r.stage.Execute(op, stmt, 0, nil)
		}
	} else {
		result, err = r.stage.Execute(op, stmt, 0, nil)
	}
	operationReplay(op).defaultReplay = false
	operationReplay(op).largeObjectAuthority = false
	operationReplay(op).partitionAttach = false
	operationReplay(op).partitionCheck = nil
	operationReplay(op).partitionUsingIndex = nil
	operationReplay(op).partitionUnique = nil
	if result.Rows != nil {
		closeErr := result.Rows.Close()
		if err == nil {
			err = closeErr
		}
	}
	operationReplay(op).commentAuthority = false
	op.DetachNotices()
	return control.Outcome{Created: result.created}, err
}
