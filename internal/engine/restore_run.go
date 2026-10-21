package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
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
	snapshots         []materializedSnapshot

	// Table, NOT NULL and foreign key captures.
	pendingForeignKeys      []table.RestoredForeignKeys
	notNullOrigins          map[ids.OID]table.RestoredNotNull
	pairedNotNull           table.RestoredNotNull
	pairedPhysical          *restoredPhysicalNotNull
	deferredPhysical        *restoredPhysicalNotNull
	physicalIndexes         map[ids.ObjectAddress]*catalog.Relation
	physicalForeignProvider *restoredPhysicalForeignKeyProvider

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
