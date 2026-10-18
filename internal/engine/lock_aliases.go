package engine

import "stuntgres/internal/engine/lock"

// Temporary aliases for the lock package, kept to make the move one small diff.
// Engine-split step 4c removes them.
type (
	completionLockPurpose  = lock.CompletionPurpose
	lockResource           = lock.Resource
	lockWaiter             = lock.Waiter
	multiEvent             = lock.MultiEvent
	multiMode              = lock.MultiMode
	multiTuple             = lock.MultiTuple
	pgLockRecord           = lock.Record
	relationHold           = lock.Hold
	relationLockCheckpoint = lock.Checkpoint
	relationLockMode       = lock.Mode
	relationLockPolicy     = lock.Policy
	relationRequest        = lock.Request
	relationTicket         = lock.Ticket
)

const (
	accessShare           = lock.AccessShare
	relationRowShare      = lock.RowShare
	rowExclusive          = lock.RowExclusive
	shareUpdateExclusive  = lock.ShareUpdateExclusive
	relationShare         = lock.Share
	shareRowExclusive     = lock.ShareRowExclusive
	relationExclusive     = lock.Exclusive
	accessExclusive       = lock.AccessExclusive
	keyShare              = lock.ForKeyShare
	rowShare              = lock.ForShare
	noKeyUpdate           = lock.ForNoKeyUpdate
	rowUpdate             = lock.ForUpdate
	uniqueClaim           = lock.UniqueClaim
	transactionCompletion = lock.TransactionCompletion
	advisoryShared        = lock.AdvisoryShare
	advisoryExclusive     = lock.AdvisoryExclusive
	typeExclusive         = lock.TypeExclusive

	advisoryBigintResource = lock.AdvisoryBigintResource
	advisoryPairResource   = lock.AdvisoryPairResource

	completionVirtual     = lock.CompletionVirtual
	completionTransaction = lock.CompletionTransaction

	multiKeyShare     = lock.MultiKeyShare
	multiShare        = lock.MultiShare
	multiNoKeyLock    = lock.MultiNoKeyLock
	multiUpdateLock   = lock.MultiUpdateLock
	multiNoKeyAction  = lock.MultiNoKeyAction
	multiUpdateAction = lock.MultiUpdateAction

	pgRelationLock    = lock.RelationRecord
	pgObjectLock      = lock.ObjectRecord
	pgAdvisoryLock    = lock.AdvisoryRecord
	pgVirtualLock     = lock.VirtualRecord
	pgTransactionLock = lock.TransactionRecord
	pgPredicateLock   = lock.PredicateRecord
)

var (
	errRelationOwnerEnded  = lock.ErrOwnerEnded
	errRelationUnavailable = lock.ErrUnavailable
)
