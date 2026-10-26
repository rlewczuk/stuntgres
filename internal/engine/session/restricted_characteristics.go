package session

import "stuntgres/internal/parser"

// EffectiveReadOnly is read under the same owner serialization as the ordinary
// transaction mode. Invocation images are serial and never published to peers.
func (op *Operation) EffectiveReadOnly() bool {
	if op.Restricted != nil {
		return op.EffectiveSettings().CurrentReadOnly
	}
	return op.Session.Transactions.ReadOnly
}

// AssignInvocationModes reuses the ordinary admission rules without copying a
// mutex or mutating the real transaction owner, session projections or SSI.
func (op *Operation) AssignInvocationModes(modes []parser.TransactionMode) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	store := op.EffectiveSettings()
	t := op.Session.Transactions
	t.Mu.Lock()
	defer t.Mu.Unlock()
	store.Mu.Lock()
	defer store.Mu.Unlock()
	candidate := TransactionOwner{Isolation: IsolationFromText(store.CurrentIsolation), ReadOnly: store.CurrentReadOnly, Deferrable: store.CurrentDeferrable, SnapshotSet: t.SnapshotSet, Savepoints: t.Savepoints}
	if err := candidate.AssignModes(modes); err != nil {
		return err
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	store.CurrentIsolation, store.CurrentReadOnly, store.CurrentDeferrable = IsolationText(candidate.Isolation), candidate.ReadOnly, candidate.Deferrable
	return nil
}
