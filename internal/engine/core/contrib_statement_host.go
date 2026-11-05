package core

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
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
	_ contrib.Host[AuthorizationView] = (*statementHost)(nil)
	_ contrib.Authority               = AuthorizationView{}
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

func (h *statementHost) CheckGinAsOwner(parent *catalog.Relation, capture index.AmcheckCapture) error {
	return h.scope().WithMaintenanceOwner(parent, func(evaluation *exec.Context) error { return index.CheckGin(evaluation, capture) })
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

// contribHost serves exec.ContribRuntime over the statement host. Contrib
// keeps no state of its own, so each method enters the runtime with the host.

func (h *contribHost) GenericFileList(kind functions.GenericFileListKind, dirname string) error {
	return contrib.GenericFileList[AuthorizationView](h.host(), kind, dirname)
}

func (h *contribHost) GenericFileRead(kind functions.GenericFileReadKind, filename string, length int64, hasLength bool) error {
	return contrib.GenericFileRead[AuthorizationView](h.host(), kind, filename, length, hasLength)
}

func (h *contribHost) GenericFileTmpdir(oid uint32) error {
	return contrib.GenericFileTmpdir[AuthorizationView](h.host(), oid)
}

func (h *contribHost) Pgrowlocks(text string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	return contrib.Pgrowlocks[AuthorizationView](h.host(), text, result)
}

func (h *contribHost) ValidatePgrowlocks(n plan.InstalledPgrowlocksExpr) error {
	return contrib.ValidatePgrowlocks[AuthorizationView](h.host(), n)
}

func (h *contribHost) ValidateDblinkGetPkey(n plan.InstalledDblinkGetPkeyExpr) error {
	return contrib.ValidateDblinkGetPkey[AuthorizationView](h.host(), n)
}

func (h *contribHost) DblinkGetPkey(n plan.InstalledDblinkGetPkeyExpr, name string) ([]string, error) {
	return contrib.DblinkGetPkey[AuthorizationView](h.host(), n, name)
}

func (h *contribHost) ValidatePgpArmorHeaders(n plan.InstalledPgpArmorHeadersExpr) error {
	return contrib.ValidatePgpArmorHeaders[AuthorizationView](h.host(), n)
}

func (h *contribHost) ValidateTablefuncNormal(n plan.InstalledTablefuncNormalExpr) error {
	return contrib.ValidateTablefuncNormal[AuthorizationView](h.host(), n)
}

func (h *contribHost) ValidateConnectby(n plan.InstalledConnectbyExpr) error {
	return contrib.ValidateConnectby[AuthorizationView](h.host(), n)
}

func (h *contribHost) Connectby(request functions.ConnectbyRequest) ([]sqltype.Value, error) {
	return contrib.Connectby[AuthorizationView](h.host(), request)
}

func (h *contribHost) TablefuncCrosstab(text, categories string, hash bool, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	return contrib.TablefuncCrosstab[AuthorizationView](h.host(), text, categories, hash, result)
}

func (h *contribHost) ValidateTablefuncCrosstab(n plan.InstalledTablefuncCrosstabExpr) error {
	return contrib.ValidateTablefuncCrosstab[AuthorizationView](h.host(), n)
}

func (h *contribHost) VerifyHeapam(args [6]sqltype.Value, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	return contrib.VerifyHeapam[AuthorizationView](h.host(), args, result)
}

func (h *contribHost) ValidateVerifyHeapam(n plan.InstalledVerifyHeapamExpr) error {
	return contrib.ValidateVerifyHeapam[AuthorizationView](h.host(), n)
}

func (h *contribHost) Amcheck(n plan.InstalledAmcheckExpr, oid uint32, flags [3]bool) error {
	return contrib.Amcheck[AuthorizationView](h.host(), n, oid, flags)
}

func (h *contribHost) PgstattupleStats(n plan.InstalledPgstattupleStatsExpr, input sqltype.Value) ([8]int64, [2]float64, error) {
	return contrib.PgstattupleStats[AuthorizationView](h.host(), n, input)
}

func (h *contribHost) Pgstattuple(n plan.InstalledPgstattupleExpr, input sqltype.Value) (storage.HeapStatistics, error) {
	return contrib.Pgstattuple[AuthorizationView](h.host(), n, input)
}

func (h *contribHost) DblinkConnection(n plan.InstalledDblinkConnectionExpr, values [2]sqltype.Value, caller authorization.PrincipalRef) (sqltype.Value, error) {
	return h.host().scope().dblinkConnection(n, values, caller)
}

func (h *contribHost) DblinkBuildSQLDelete(n plan.InstalledDblinkBuildSQLDeleteExpr, values [4]sqltype.Value) (string, error) {
	return contrib.DblinkBuildSQLDelete[AuthorizationView](h.host(), n, values)
}
func (h *contribHost) DblinkBuildSQLInsert(n plan.InstalledDblinkBuildSQLInsertExpr, values [5]sqltype.Value) (string, error) {
	return contrib.DblinkBuildSQLInsert[AuthorizationView](h.host(), n, values)
}
func (h *contribHost) DblinkBuildSQLUpdate(n plan.InstalledDblinkBuildSQLUpdateExpr, values [5]sqltype.Value) (string, error) {
	return contrib.DblinkBuildSQLUpdate[AuthorizationView](h.host(), n, values)
}

func (h *contribHost) PgFreespace(n plan.InstalledPgFreespaceExpr, oid uint32, block int64) (storage.FreespaceLayout, error) {
	return contrib.PgFreespace[AuthorizationView](h.host(), n, oid, block)
}
func (h *contribHost) PgFreespaceExtent(n plan.InstalledPgFreespaceExpr, oid uint32) (storage.FreespaceLayout, bool, error) {
	return contrib.PgFreespaceExtent[AuthorizationView](h.host(), n, oid)
}
func (h *contribHost) Pgvisibility(n plan.InstalledPgvisibilityExpr, oid uint32, block int64) (storage.VisibilityMap, error) {
	return contrib.Pgvisibility[AuthorizationView](h.host(), n, oid, block)
}
func (h *contribHost) PgvisibilityCheck(n plan.InstalledPgvisibilityExpr, oid uint32) ([]ids.VersionID, error) {
	return contrib.PgvisibilityCheck[AuthorizationView](h.host(), n, oid)
}
func (h *contribHost) PgvisibilityTruncate(n plan.InstalledPgvisibilityExpr, oid uint32) error {
	return contrib.PgvisibilityTruncate[AuthorizationView](h.host(), n, oid)
}
func (h *contribHost) Pgsurgery(n plan.InstalledPgsurgeryExpr, oid uint32, tids sqltype.Value) error {
	return contrib.Pgsurgery[AuthorizationView](h.host(), n, oid, tids)
}

func (h *contribHost) Pageinspect(n plan.InstalledPageinspectExpr, oid uint32) ([]sqltype.BrinStoredColumn, error) {
	return contrib.Pageinspect[AuthorizationView](h.host(), n, oid)
}

func (h *contribHost) PageinspectHeap(n plan.InstalledPageinspectExpr, oid uint32) ([]sqltype.HeapStoredColumn, error) {
	return contrib.PageinspectHeap[AuthorizationView](h.host(), n, oid)
}
