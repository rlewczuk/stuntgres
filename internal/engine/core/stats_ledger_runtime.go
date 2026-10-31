package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

// Settlement consumes unresolved ownership exactly once, without rechecking the
// policy captured at mutation. Only completed work enters reportable pending.
func settleStatsLedger(e *Engine, sess *session.Session, ledger *statistics.Ledger, commit bool) {
	for key, account := range *ledger {
		var event statistics.Fact
		if commit {
			event = account.Committed()
		} else {
			event = account.AbortedFrom(0)
		}
		if event.Truncated || event.Inserts != 0 || event.Updates != 0 || event.Deletes != 0 {
			PayloadOf(sess).statsWriter.Add(&e.Statistics, key, event)
		}
	}
	*ledger = nil
}

// A completed aborted child reports only its own frames, never its surviving
// parent's counters. The caller then restores the immutable savepoint root.
func abortStatsFrames(e *Engine, sess *session.Session, ledger statistics.Ledger, level int) {
	for key, account := range ledger {
		event := account.AbortedFrom(level)
		if event.Inserts != 0 || event.Updates != 0 || event.Deletes != 0 {
			PayloadOf(sess).statsWriter.Add(&e.Statistics, key, event)
		}
	}
}

// Called only at successful publication, after PREPARE's ownership transfer.
// Combine same-level segments before resolving truncate/pre-truncate state.
func publishTransactionStats(e *Engine, sess *session.Session, ref ids.TxRef, phase *session.PreparedPublication, scope *StatementScope) {
	if phase != nil {
		settleStatsLedger(e, sess, &phase.Entry.Stats, true)
		retireStatsObjects(e, sess, phase.Entry.Ref.Epoch(), true, phase.Entry.Catalog)
		return
	}
	if owner := session.TxOwnerOf(ref); owner != nil {
		if scope != nil {
			owner.Stats.Take(&scope.Stage.Stats)
		}
		settleStatsLedger(e, sess, &owner.Stats, true)
		retireStatsObjects(e, sess, ref.Epoch(), true, owner.Catalog)
		if scope != nil && scope.Catalog != owner.Catalog {
			retireStatsObjects(e, sess, ref.Epoch(), true, scope.Catalog)
		}
	} else if scope != nil {
		settleStatsLedger(e, sess, &scope.Stage.Stats, true)
		retireStatsObjects(e, sess, ref.Epoch(), true, scope.Catalog)
	}
}
