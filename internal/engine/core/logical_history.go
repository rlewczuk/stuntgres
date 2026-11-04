package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// History records are appended at the publishers' callback-free installation
// points, under Engine.Mu, in the same order the commits become visible. A
// database without a logical slot keeps no history.

// logicalEffects returns the changes a publication commits: the owner's merged
// statements followed by the publishing statement's own. Prepared finishes
// carry their changes in the prepared entry instead.
func logicalEffects(op *session.Operation, phase *session.PreparedPublication, scope *StatementScope) []logical.Change {
	if phase != nil || op == nil || op.Session == nil || op.Session.Transactions == nil {
		return nil
	}
	owner := op.Session.Transactions
	if scope == nil || scope.Owner != owner {
		return owner.LogicalChanges
	}
	return owner.MergeLogicalChanges(scope.logicalChanges)
}

// journalChangesCatalog reports catalog writes, which PostgreSQL logs with the
// transaction's XID and decodes as an otherwise empty transaction. Sequence
// advancement alone is not such a write.
func journalChangesCatalog(c *session.Journal) bool {
	return c != nil && (len(c.Descriptors) != 0 || len(c.Names) != 0 || len(c.DefaultACLs) != 0 || len(c.InitialPrivileges) != 0 ||
		len(c.Storage) != 0 || len(c.Statistics) != 0 || c.Roles != nil || index.JournalOf(c.Indexes).Pending())
}

// logicalCommitIdentity is the XID, timestamp and replication origin of a
// commit, prepare or prepared-transaction finish.
type logicalCommitIdentity struct {
	xid    uint32
	stamp  int64
	origin uint32
}

// recordLogicalCommitLocked records an ordinary commit, or the COMMIT PREPARED
// of phase. PREPARE itself is recorded at installPreparedLocked.
func (e *Engine) recordLogicalCommitLocked(commit logicalCommitIdentity, phase *session.PreparedPublication, changes []logical.Change, c *session.Journal) {
	if phase != nil {
		if !phase.Preparing {
			e.appendLogicalFinishLocked(phase.Entry, true, commit)
		}
		return
	}
	e.appendLogicalCommitLocked(commit, changes, journalChangesCatalog(c))
}

// appendLogicalCommitLocked records a committed transaction that has captured
// changes or catalog writes.
func (e *Engine) appendLogicalCommitLocked(commit logicalCommitIdentity, changes []logical.Change, catalogWrites bool) {
	if !e.logicalCaptureLocked() || commit.xid == 0 || len(changes) == 0 && !catalogWrites {
		return
	}
	txn := e.logicalTransactionLocked(commit.xid, changes)
	e.logicalHistory.Append(logical.Record{Kind: logical.Commit, LSN: e.Cluster.logical.lsn.Allocate(), Time: commit.stamp, Origin: commit.origin, Txn: txn})
}

// appendLogicalPrepareLocked records PREPARE TRANSACTION and keeps the content
// on the entry for its later COMMIT or ROLLBACK PREPARED.
func (e *Engine) appendLogicalPrepareLocked(entry *session.PreparedTransaction, prepare logicalCommitIdentity) {
	if !e.logicalCaptureLocked() || prepare.xid == 0 || len(entry.LogicalChanges) == 0 && !journalChangesCatalog(entry.Catalog) {
		return
	}
	txn := e.logicalTransactionLocked(prepare.xid, entry.LogicalChanges)
	txn.GID, txn.PrepareTime = entry.GID, prepare.stamp
	txn.PrepareLSN = e.Cluster.logical.lsn.Allocate()
	entry.LogicalTxn = txn
	e.logicalHistory.Append(logical.Record{Kind: logical.Prepare, LSN: txn.PrepareLSN, Time: prepare.stamp, Origin: prepare.origin, Txn: txn})
}

// appendLogicalFinishLocked records COMMIT PREPARED or ROLLBACK PREPARED of a
// prepared transaction whose PREPARE was recorded.
func (e *Engine) appendLogicalFinishLocked(entry *session.PreparedTransaction, commit bool, finish logicalCommitIdentity) {
	if !e.logicalCaptureLocked() || entry == nil || entry.LogicalTxn == nil {
		return
	}
	kind := logical.RollbackPrepared
	if commit {
		kind = logical.CommitPrepared
	}
	e.logicalHistory.Append(logical.Record{Kind: kind, LSN: e.Cluster.logical.lsn.Allocate(), Time: finish.stamp, Origin: finish.origin, Txn: entry.LogicalTxn})
}

func (e *Engine) logicalTransactionLocked(xid uint32, changes []logical.Change) *logical.Transaction {
	txn := &logical.Transaction{XID: xid, Changes: changes}
	if len(changes) != 0 {
		txn.FirstLSN = changes[0].LSN
	} else {
		// The first record of a catalog-only transaction.
		txn.FirstLSN = e.Cluster.logical.lsn.Allocate()
	}
	return txn
}

// captureLogicalInitialRowsLocked records CREATE TABLE AS rows as inserts into
// the new table. view includes the staged relation.
func (e *Engine) captureLogicalInitialRowsLocked(view catalog.Read, d *catalog.Relation, versions []storage.RowVersion) []logical.Change {
	if !e.logicalCaptureLocked() {
		return nil
	}
	relation := logicalRelationOf(view, d)
	if relation == nil {
		return nil
	}
	changes := make([]logical.Change, len(versions))
	for i, version := range versions {
		changes[i] = logical.Change{LSN: e.Cluster.logical.lsn.Allocate(), Kind: logical.Insert, Relation: relation, New: version.Values}
	}
	return changes
}
