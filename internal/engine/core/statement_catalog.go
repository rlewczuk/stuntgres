package core

import "stuntgres/internal/engine/session"

// PendingCatalog includes earlier effects of this same utility, not just the
// transaction's predecessor. Nested callbacks and main DDL share this journal.
func (s *StatementScope) PendingCatalog() *session.Journal {
	if s.Catalog != nil {
		return s.Catalog
	}
	return s.Owner.Catalog
}

// Only an already-completed standalone utility can skip ordinary Finish. A
// utility with a private catalog still owes atomic catalog/storage/row publication.
func (s *StatementScope) CompleteUtility() { s.Completed = s.Catalog == nil }

// OperationCatalog returns the current unfinished statement's journal, falling
// back to its transaction owner after the statement detaches.
func OperationCatalog(op *session.Operation) *session.Journal {
	if op == nil || op.Session == nil || op.Session.Transactions == nil {
		return nil
	}
	if scope := ActiveScopeOfLease(op.Lease); scope != nil {
		return scope.PendingCatalog()
	}
	return op.Session.Transactions.Catalog
}

func installOperationCatalog(op *session.Operation, journal *session.Journal) {
	if scope := ActiveScopeOfLease(op.Lease); scope != nil && scope.Catalog != nil {
		scope.Catalog = journal
		return
	}
	op.Session.Transactions.Catalog = journal
}

func (a AuthorizationView) PendingCatalog() *session.Journal {
	if a.Statement != nil && !a.Statement.Finished {
		return a.Statement.PendingCatalog()
	}
	if a.Session != nil && a.Session.Transactions != nil {
		return a.Session.Transactions.Catalog
	}
	return nil
}
