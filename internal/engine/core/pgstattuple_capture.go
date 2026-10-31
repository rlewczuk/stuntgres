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
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgstattupleAuthority[A Authority](auth A, n plan.InstalledPgstattupleExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) { return ids.ErrCatalogReplaced }
	if err := extension.ValidatePgstattupleIdentity(view.Catalog, current, n.Ref); err != nil { return err }
	boundary := n.Pgstattuple
	boundary.RoutineReference = current
	if err := extension.ValidatePgstattupleCall(view.Catalog, current, boundary); err != nil { return err }
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return err }
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
		if ns, ok := v.Namespace(target.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token { return sqlError("0A000", "cannot access temporary tables of other sessions") }
	}
	if target.SQLIndex != nil && kind == functions.HeapStatisticsExact {
		if !target.SQLIndex.Valid { return sqlError("55000", fmt.Sprintf("index %q is not valid", target.Name())) }
		method := target.SQLIndex.MethodIdentity()
		if method != 403 && method != 405 && method != 783 {
			name := "unknown"
			if am, ok := v.AccessMethod(method); ok { name = am.Name() }
			return sqlError("0A000", fmt.Sprintf("index %q (%s index) is not supported", target.Name(), name))
		}
	}
	if target.SQLIndex == nil && target.Sequence == nil && kind != functions.HeapStatisticsPages && target.TableMethod != 0 && target.TableMethod != 2 { return sqlError("0A000", "only heap AM is supported") }
	return nil
}

// Pgstattuple is the exact and approximate tuple and page inquiries.
func Pgstattuple[A Authority](h Host[A], n plan.InstalledPgstattupleExpr, input sqltype.Value) (storage.HeapStatistics, error) {
	if err := h.Active(); err != nil {
		return storage.HeapStatistics{}, err
	}
	h.ReacquireEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgstattupleAuthority(auth, n)
	}
	var path []ids.ObjectAddress
	if err == nil && n.Input == sqltype.Text {
		path, err = auth.ReferencePath()
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return storage.HeapStatistics{}, err
	}
	var oid uint32
	if n.Input == sqltype.Text {
		text, failure := input.Text()
		err = failure
		if err == nil {
			oid, err = h.ClassInput(auth, path).ResolveRelationName(h.Operation().Context(), text)
		}
	} else {
		oid, err = input.ClassOID()
	}
	if err != nil {
		return storage.HeapStatistics{}, err
	}
	epoch := auth.Access().Catalog.Epoch()
	h.ReacquireEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil && auth.Access().Catalog.Epoch() != epoch {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = pgstattupleAuthority(auth, n)
	}
	var target, parent *catalog.Relation
	var store *storage.Relation
	if err == nil {
		target, parent, err = amcheckTargetIdentity(auth.Access().Catalog, oid)
	}
	if err == nil {
		address := target.Address()
		if parent != nil {
			address = parent.Address()
		}
		store = h.PrivateStorageLocked(address)
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return storage.HeapStatistics{}, err
	}
	ref := h.CatalogDomain().Ref(epoch, target.Address())
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil {
		return storage.HeapStatistics{}, err
	}
	// Only the caller's stage is reachable here. No peer Admission is taken.
	address := target.Address()
	if parent != nil {
		address = parent.Address()
	}
	writes, deletes := h.ColumnProjectionRoots(address)
	owners, err := h.LockHeapStatisticsCohort()
	if err != nil {
		return storage.HeapStatistics{}, err
	}
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil && auth.Access().Catalog.Epoch() != epoch {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = pgstattupleAuthority(auth, n)
	}
	if err == nil && (auth.Access().Catalog.Get(target.Address()) != target || h.PrivateStorageLocked(address) != store || parent != nil && auth.Access().Catalog.Get(parent.Address()) != parent) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		h.MarkTemporaryTargetLocked(ref)
		err = PgstattupleTargetCheckable(auth.Access().Catalog, target, h.Operation().Session.SessionToken, n.Kind)
	}
	var facts []storage.HeapTupleFact
	var indexCapture index.PgstattupleStatsCapture
	if err == nil {
		switch {
		case target.Sequence != nil:
			generator := h.EngineStorageLocked().Sequences[target.Address()]
			if pending := h.PendingCatalog(); pending != nil && pending.Generators[target.Address()] != nil {
				generator = pending.Generators[target.Address()]
			}
			if generator == nil {
				err = ids.ErrCatalogReplaced
			}
		case target.SQLIndex != nil:
			if store == nil {
				err = ids.ErrCatalogReplaced
				break
			}
			root := index.EffectiveRoot(h.EngineStorageLocked().IndexRoots, h.PendingCatalog(), target.Address())
			physicalWrites := h.HeapStatisticsPrivateWritesLocked(owners, store, address, writes)
			indexCapture = index.PgstattupleStatsCapture{Index: target, Parent: parent, Root: root, Store: *store, Writes: physicalWrites, Deletes: deletes, Frontier: h.CommitSequenceLocked()}
		default:
			if store == nil {
				err = ids.ErrCatalogReplaced
				break
			}
			facts = h.HeapStatisticsFactsLocked(owners, store, address, writes, deletes)
		}
	}
	h.UnlockHeapStatisticsCohortLocked(owners)
	if err != nil {
		return storage.HeapStatistics{}, err
	}
	var stats storage.HeapStatistics
	switch {
	case target.Sequence != nil:
		// One genuine generator has one fixed-shape physical state tuple:
		// header, null bitmap, aligned Last and Called datum slots.
		stats, err = storage.HeapStatisticsLayout(h.Operation().Context(), 1, 0, 48, 0, false)
	case target.SQLIndex != nil:
		stats, err = index.CaptureTupleStatistics(h.Operation().Context(), indexCapture, n.Kind == functions.HeapStatisticsPages)
	default:
		stats, err = storage.CaptureHeapStatistics(h.Operation().Context(), facts, h.TransactionRef(), n.Kind == functions.HeapStatisticsApprox)
	}
	if err != nil {
		return stats, err
	}
	h.ReacquireEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgstattupleAuthority(auth, n)
	}
	valid := err == nil && h.CatalogEpochLocked() == epoch && auth.Access().Catalog.Get(target.Address()) == target && (parent == nil || auth.Access().Catalog.Get(parent.Address()) == parent) && h.PrivateStorageLocked(address) == store
	h.ReleaseEngineLocked()
	if err != nil {
		return storage.HeapStatistics{}, err
	}
	if !valid {
		return storage.HeapStatistics{}, ids.ErrCatalogReplaced
	}
	if err := h.Operation().Context().Err(); err != nil {
		return storage.HeapStatistics{}, err
	}
	return stats, nil
}
