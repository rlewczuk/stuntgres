package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The contrib runtime's host is the statement host; AuthorizationView is its
// authority. Context, Operation, Environments, Active, Ticket, the nested-SQL
// capability, the pending catalog and storage reads and the Engine.Mu pair
// are shared with exec.Host, the EXPLAIN renderer and text search.
var (
	_ Host[AuthorizationView] = (*statementHost)(nil)
	_ Authority               = AuthorizationView{}
)

func (h *statementHost) RoutineAuthorizationLocked() (AuthorizationView, error) {
	return h.scope().RoutineAuthorizationLocked()
}

func (h *statementHost) WriteAuthorizationLocked() (AuthorizationView, error) {
	s := h.scope()
	return s.writeAuthorizationLocked(s.Engine())
}

func (h *statementHost) CallableAuthorizationLocked() (AuthorizationView, error) {
	s := h.scope()
	return s.callableAuthorizationLocked(s.Engine())
}

func (h *statementHost) ResolveRelationName(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	return resolveRelationSnapshot(auth, name, true)
}

func (h *statementHost) ClassInput(auth AuthorizationView, path []ids.ObjectAddress) regobj.ClassInput {
	return regobj.NewClassInput(inputCapture(auth, path))
}

func (h *statementHost) TypeDiagnosticName(auth AuthorizationView) (func(sqltype.ID) string, error) {
	return CaptureTypeDiagnosticName(auth)
}

// ReacquireOwnerEngine takes Owner.Mu, then Engine.Mu;
// ReleaseOwnerEngineLocked releases Engine.Mu, then Owner.Mu.
func (h *statementHost) ReacquireOwnerEngine() {
	s := h.scope()
	s.Owner.Mu.Lock()
	s.Engine().Mu.Lock()
}

func (h *statementHost) ReleaseOwnerEngineLocked() {
	s := h.scope()
	s.Engine().Mu.Unlock()
	s.Owner.Mu.Unlock()
}

func (h *statementHost) CatalogEpochLocked() ids.CatalogEpoch {
	return h.scope().Engine().Catalog.State.Epoch()
}

func (h *statementHost) OwnerCatalog() *session.Journal { return h.Owner.Catalog }

func (h *statementHost) JournalStorageLocked(changes *session.Journal, address ids.ObjectAddress) *storage.Relation {
	return h.scope().Engine().PrivateStorageLocked(changes, address)
}

func (h *statementHost) SequenceGeneratorLocked(address ids.ObjectAddress) *storage.SequenceGenerator {
	s := h.scope()
	return s.Engine().SequenceGeneratorLocked(s, address)
}

func (h *statementHost) CommitSequenceLocked() ids.CommitSeq {
	return h.scope().Engine().Visibility.CommitSeq()
}

func (h *statementHost) ColumnProjectionRoots(address ids.ObjectAddress) ([]storage.RowVersion, *visibility.PrivateDeletes) {
	return h.scope().ColumnProjectionRoots(address)
}

func (h *statementHost) TransactionRef() ids.TxRef { return h.TxRef }

func (h *statementHost) MarkTemporaryTargetLocked(ref ids.ObjectRef) {
	s := h.scope()
	s.Engine().markTemporaryAccessLocked(s, ref, temporaryTargetAttempt)
}

func (h *statementHost) RelationDataViewLocked(ref ids.ObjectRef) (view *visibility.View, local bool, err error) {
	s, e := h.scope(), h.scope().Engine()
	if s.View != nil {
		if _, captured := s.View.Load().Committed[ref.Address]; !captured {
			view, err = s.CaptureDataViewsAtLocked(e, []ids.ObjectRef{ref}, false, s.View.Load())
			local = true
		} else {
			view, err = s.acquireDataViewLocked(e, ref)
		}
	} else {
		view, err = s.acquireDataViewLocked(e, ref)
	}
	return view, local, err
}

func (h *statementHost) LockManager() *lock.Manager { return h.scope().Engine().Locks }

func (h *statementHost) AcquireRelationContribution(ref ids.ObjectRef, mode lock.Mode, contribution *lock.RelationContribution) error {
	s, e := h.scope(), h.scope().Engine()
	resource := lock.Resource{ObjectRef: ref}
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquireRelation(s.Op.Context(), s.LockTicket, resource, mode, policy, contribution)
	if request != nil {
		s.admissionWaited = true
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	s.Owner.Mu.Lock()
	implicit := s.Owner.State == session.TxImplicit
	s.Owner.Mu.Unlock()
	if implicit && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	return nil
}

func (h *statementHost) PgrowlocksReadmitLocked(auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation) error {
	s := h.scope()
	return s.pgrowlocksReadmitLocked(s.Engine(), auth, name, ref, descriptor, stored)
}

func (h *statementHost) VerifyHeapamReadmitLocked(auth AuthorizationView, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation, sequence *storage.SequenceGenerator) error {
	s := h.scope()
	return s.verifyHeapamReadmitLocked(s.Engine(), auth, ref, descriptor, stored, sequence)
}

func (h *statementHost) LockHeapStatisticsCohort() ([]*session.TransactionOwner, error) {
	s, e := h.scope(), h.scope().Engine()
	for {
		if err := s.Op.Context().Err(); err != nil {
			return nil, err
		}
		owners := e.heapStatisticsOwners(s.Owner)
		for _, owner := range owners {
			owner.Mu.Lock()
		}
		e.Mu.Lock()
		if !e.heapStatisticsCohortCurrent(owners, s.Owner) {
			e.Mu.Unlock()
			for i := len(owners) - 1; i >= 0; i-- {
				owners[i].Mu.Unlock()
			}
			continue
		}
		return owners, nil
	}
}

func (h *statementHost) UnlockHeapStatisticsCohortLocked(owners []*session.TransactionOwner) {
	h.scope().Engine().Mu.Unlock()
	for i := len(owners) - 1; i >= 0; i-- {
		owners[i].Mu.Unlock()
	}
}

func (h *statementHost) HeapStatisticsFactsLocked(owners []*session.TransactionOwner, store *storage.Relation, address ids.ObjectAddress, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) []storage.HeapTupleFact {
	s := h.scope()
	return s.Engine().heapStatisticsFactsLocked(s, owners, store, address, writes, deletes)
}

func (h *statementHost) HeapStatisticsPrivateWritesLocked(owners []*session.TransactionOwner, store *storage.Relation, address ids.ObjectAddress, writes []storage.RowVersion) []storage.RowVersion {
	s := h.scope()
	return s.Engine().heapStatisticsPrivateWritesLocked(s, owners, store, address, writes)
}

// CheckBtreeAsOwner keeps the maintenance-owner work closure in core: passed
// through the contrib host interface it would escape.
func (h *statementHost) CheckBtreeAsOwner(parent *catalog.Relation, capture index.AmcheckCapture, options index.AmcheckOptions) error {
	return h.scope().WithMaintenanceOwner(parent, func(evaluation *exec.Context) error { return index.CheckBtree(evaluation, capture, options) })
}

// GenericFileAuthority borrows current authority and the path settings in
// Engine -> Settings -> Session order, matching setting reads.
func (h *statementHost) GenericFileAuthority() (dataDirectory, logDirectory string, privileged bool, err error) {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return "", "", false, err
	}
	role, ok := auth.Roles.Lookup("pg_read_server_files")
	if !ok {
		return "", "", false, fmt.Errorf("stuntgres: missing pg_read_server_files role")
	}
	privileged, err = auth.Roles.InheritsRole(auth.State.CurrentUser, role.Ref)
	if err != nil {
		return "", "", false, err
	}
	owner := s.Op.EffectiveSettings()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := owner.AdmitLocked(DomainOf(e), s.Op); err != nil {
		return "", "", false, err
	}
	return owner.Value("data_directory"), owner.Value("log_directory"), privileged, nil
}

func (h *statementHost) InquiryCatalog() (catalog.Read, error) { return h.catalogInquiryCatalog() }
