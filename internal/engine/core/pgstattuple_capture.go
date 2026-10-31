package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgstattupleAuthority(auth AuthorizationView, n plan.InstalledPgstattupleExpr) error {
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != auth.Catalog.Owner().Domain || n.Ref.Epoch != auth.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) { return ids.ErrCatalogReplaced }
	if err := extension.ValidatePgstattupleIdentity(auth.Catalog, current, n.Ref); err != nil { return err }
	boundary := n.Pgstattuple
	boundary.RoutineReference = current
	if err := extension.ValidatePgstattupleCall(auth.Catalog, current, boundary); err != nil { return err }
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return err }
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}

// PgstattupleTargetCheckable is descriptor-only dispatch shared by physical
// capture and native descriptor witnesses. It never manufactures AM storage.
func PgstattupleTargetCheckable(v catalog.Read, target *catalog.Relation, token ids.SessionToken, kind functions.HeapStatisticsKind) error {
	label := ""
	switch target.RelationKind {
	case catalog.ViewRelation: label = "views"
	case catalog.ForeignRelation: label = "foreign tables"
	case catalog.CompositeRelation: label = "composite types"
	case catalog.PartitionedRelation: label = "partitioned tables"
	case catalog.IndexRelation:
		if target.SQLIndex != nil {
			if parent, ok := v.Relation(target.SQLIndex.Parent); ok && parent.Partition != nil { label = "partitioned indexes" }
		}
		if label == "" && kind == functions.HeapStatisticsApprox { label = "indexes" }
	case catalog.SequenceRelation:
		if kind == functions.HeapStatisticsApprox { label = "sequences" }
	case catalog.TableRelation, catalog.MaterializedViewRelation, catalog.CatalogRelation:
	default: label = "composite types"
	}
	if label != "" {
		code, message := "0A000", fmt.Sprintf("cannot get tuple-level statistics for relation %q", target.Name())
		if kind == functions.HeapStatisticsApprox { message = fmt.Sprintf("relation %q is of wrong relation kind", target.Name()) }
		if kind == functions.HeapStatisticsPages { code, message = "42809", fmt.Sprintf("cannot get page count of relation %q", target.Name()) }
		return &sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:code, Message:message, Detail:"This operation is not supported for "+label+"."}
	}
	if kind != functions.HeapStatisticsPages {
		if ns, ok := v.Namespace(target.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token { return SQLError("0A000", "cannot access temporary tables of other sessions") }
	}
	if target.SQLIndex != nil && kind == functions.HeapStatisticsExact {
		if !target.SQLIndex.Valid { return SQLError("55000", fmt.Sprintf("index %q is not valid", target.Name())) }
		method := target.SQLIndex.MethodIdentity()
		if method != 403 && method != 405 && method != 783 {
			name := "unknown"
			if am, ok := v.AccessMethod(method); ok { name = am.Name() }
			return SQLError("0A000", fmt.Sprintf("index %q (%s index) is not supported", target.Name(), name))
		}
	}
	if target.SQLIndex == nil && target.Sequence == nil && kind != functions.HeapStatisticsPages && target.TableMethod != 0 && target.TableMethod != 2 { return SQLError("0A000", "only heap AM is supported") }
	return nil
}

func (h *contribHost) Pgstattuple(n plan.InstalledPgstattupleExpr, input sqltype.Value) (storage.HeapStatistics, error) {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil { return storage.HeapStatistics{}, err }
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err == nil { err = pgstattupleAuthority(auth, n) }
	var path []ids.ObjectAddress
	if err == nil && n.Input == sqltype.Text { path, err = ReferencePath(auth) }
	e.Mu.Unlock()
	if err != nil { return storage.HeapStatistics{}, err }
	var oid uint32
	if n.Input == sqltype.Text {
		text, failure := input.Text()
		err = failure
		if err == nil { oid, err = regobj.NewClassInput(inputCapture(auth, path)).ResolveRelationName(s.Op.Context(), text) }
	} else { oid, err = input.ClassOID() }
	if err != nil { return storage.HeapStatistics{}, err }
	epoch := auth.Catalog.Epoch()
	e.Mu.Lock()
	auth, err = s.RoutineAuthorizationLocked()
	if err == nil && auth.Catalog.Epoch() != epoch { err = ids.ErrCatalogReplaced }
	if err == nil { err = pgstattupleAuthority(auth, n) }
	var target, parent *catalog.Relation
	var store *storage.Relation
	if err == nil { target, parent, err = amcheckTargetIdentity(auth, oid) }
	if err == nil {
		address := target.Address()
		if parent != nil { address = parent.Address() }
		store = e.PrivateStorageLocked(s.PendingCatalog(), address)
	}
	e.Mu.Unlock()
	if err != nil { return storage.HeapStatistics{}, err }
	ref := e.Catalog.Domain.Ref(epoch, target.Address())
	var contribution lock.RelationContribution
	defer e.Locks.ReleaseRelation(&contribution)
	policy := lock.Policy{TimeoutSlot:&s.Op.LockWait, Timeout:s.Op.LockTimeout, StatementDeadline:s.Op.StatementDeadline, Source:s.Op.SourceContext}
	request, err := e.Locks.StartAcquireRelation(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef:ref}, lock.AccessShare, policy, &contribution)
	if request != nil {
		s.admissionWaited = true
		if s.ReplacementRead { e.ReplacementMu.RUnlock(); s.ReplacementRead = false }
		err = request.Wait()
	}
	if err != nil { return storage.HeapStatistics{}, err }
	s.Owner.Mu.Lock()
	implicit := s.Owner.State == session.TxImplicit
	s.Owner.Mu.Unlock()
	if implicit && !s.ReplacementRead { e.ReplacementMu.RLock(); s.ReplacementRead = true }
	// Only the caller's stage is reachable here. No peer Admission is taken.
	address := target.Address()
	if parent != nil { address = parent.Address() }
	writes, deletes := s.ColumnProjectionRoots(address)
	for {
		if err := s.Op.Context().Err(); err != nil { return storage.HeapStatistics{}, err }
		owners := e.heapStatisticsOwners(s.Owner)
		for _, owner := range owners { owner.Mu.Lock() }
		e.Mu.Lock()
		if !e.heapStatisticsCohortCurrent(owners, s.Owner) {
			e.Mu.Unlock()
			for i:=len(owners)-1; i>=0; i-- { owners[i].Mu.Unlock() }
			continue
		}
		auth, err = s.RoutineAuthorizationLocked()
		if err == nil && auth.Catalog.Epoch() != epoch { err = ids.ErrCatalogReplaced }
		if err == nil { err = pgstattupleAuthority(auth, n) }
		if err == nil && (auth.Catalog.Get(target.Address()) != target || e.PrivateStorageLocked(s.PendingCatalog(), address) != store || parent != nil && auth.Catalog.Get(parent.Address()) != parent) { err = ids.ErrCatalogReplaced }
		if err == nil {
			e.markTemporaryAccessLocked(s, ref, temporaryTargetAttempt)
			err = PgstattupleTargetCheckable(auth.Catalog, target, s.Op.Session.SessionToken, n.Kind)
		}
		var facts []storage.HeapTupleFact
		var indexCapture index.PgstattupleStatsCapture
		if err == nil {
			switch {
			case target.Sequence != nil:
				generator := e.Storage.Sequences[target.Address()]
				if pending := s.PendingCatalog(); pending != nil && pending.Generators[target.Address()] != nil { generator = pending.Generators[target.Address()] }
				if generator == nil { err = ids.ErrCatalogReplaced }
			case target.SQLIndex != nil:
				if store == nil { err = ids.ErrCatalogReplaced; break }
				root := index.EffectiveRoot(e.Storage.IndexRoots, s.PendingCatalog(), target.Address())
				physicalWrites := e.heapStatisticsPrivateWritesLocked(s, owners, store, address, writes)
				indexCapture = index.PgstattupleStatsCapture{Index:target, Parent:parent, Root:root, Store:*store, Writes:physicalWrites, Deletes:deletes, Frontier:e.Visibility.CommitSeq()}
			default:
				if store == nil { err = ids.ErrCatalogReplaced; break }
				facts = e.heapStatisticsFactsLocked(s, owners, store, address, writes, deletes)
			}
		}
		e.Mu.Unlock()
		for i:=len(owners)-1; i>=0; i-- { owners[i].Mu.Unlock() }
		if err != nil { return storage.HeapStatistics{}, err }
		var stats storage.HeapStatistics
		switch {
		case target.Sequence != nil:
			// One genuine generator has one fixed-shape physical state tuple:
			// header, null bitmap, aligned Last and Called datum slots.
			stats, err = storage.HeapStatisticsLayout(s.Op.Context(), 1, 0, 48, 0, false)
		case target.SQLIndex != nil:
			stats, err = index.CaptureTupleStatistics(s.Op.Context(), indexCapture, n.Kind == functions.HeapStatisticsPages)
		default:
			stats, err = storage.CaptureHeapStatistics(s.Op.Context(), facts, s.TxRef, n.Kind == functions.HeapStatisticsApprox)
		}
		if err != nil { return stats, err }
		e.Mu.Lock()
		auth, err = s.RoutineAuthorizationLocked()
		if err == nil { err = pgstattupleAuthority(auth, n) }
		valid := err == nil && e.Catalog.State.Epoch() == epoch && auth.Catalog.Get(target.Address()) == target && (parent == nil || auth.Catalog.Get(parent.Address()) == parent) && e.PrivateStorageLocked(s.PendingCatalog(), address) == store
		e.Mu.Unlock()
		if err != nil { return storage.HeapStatistics{}, err }
		if !valid { return storage.HeapStatistics{}, ids.ErrCatalogReplaced }
		if err := s.Op.Context().Err(); err != nil { return storage.HeapStatistics{}, err }
		return stats, nil
	}
}
