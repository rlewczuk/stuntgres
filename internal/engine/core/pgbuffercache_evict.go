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
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
)

func pgBuffercacheEvictionAuthority(auth AuthorizationView, n plan.InstalledPgBuffercacheEvictionExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) { return ids.ErrCatalogReplaced }
	if err := extension.ValidatePgBuffercacheIdentity(view.Catalog, current, n.Ref); err != nil { return err }
	boundary := n.PgBuffercacheEviction
	boundary.RoutineReference = current
	if err := extension.ValidatePgBuffercacheEvictionCall(view.Catalog, current, boundary); err != nil { return err }
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return err }
	if err := auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true}); err != nil { return err }
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil { return err }
	if !actor.Superuser {
		name := extension.PgBuffercacheEvictionMembers[current.Origin.Declaration-1].Name
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be superuser to use "+name+"()"}
	}
	return nil
}

// Mutation shares the registered cluster cache with real read leases. Catalog
// and relation authority precede the cache leaf; no authority is acquired there.
func (h *contribHost) PgBuffercacheEviction(n plan.InstalledPgBuffercacheEvictionExpr, input int64) (result [3]int64, err error) {
	host := h.host()
	if err = host.Active(); err != nil { return }
	e := host.scope().Engine()
	host.ReacquireEngine()
	auth, err := host.RoutineAuthorizationLocked()
	if err == nil { err = pgBuffercacheEvictionAuthority(auth, n) }
	var target *catalog.Relation
	var targetRef ids.ObjectRef
	if err == nil && n.Kind == functions.EvictRelation {
		var ok bool
		target, ok = auth.Access().Catalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(input)})
		if !ok {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", input)}
		} else {
			targetRef = auth.Access().Ref(target.Address())
		}
	}
	host.ReleaseEngineLocked()
	if err != nil { return }
	var contribution lock.RelationContribution
	if target != nil {
		defer host.LockManager().ReleaseRelation(&contribution)
		if err = host.AcquireRelationContribution(targetRef, lock.AccessShare, &contribution); err != nil { return }
	}
	host.ReacquireEngine()
	defer host.ReleaseEngineLocked()
	auth, err = host.RoutineAuthorizationLocked()
	if err == nil { err = pgBuffercacheEvictionAuthority(auth, n) }
	if err != nil { return }
	if target != nil && auth.Access().Catalog.Get(target.Address()) != target { err = ids.ErrCatalogReplaced; return }
	if err = e.registeredLogicalBufferOwnerLocked(); err != nil { return }
	var key *logicalBufferKey
	if target != nil {
		namespace, ok := auth.Access().Catalog.Namespace(target.Namespace())
		if !ok { err = ids.ErrCatalogReplaced; return }
		if namespace.Temporary != (ids.SessionToken{}) {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "relation uses local buffers, pg_buffercache_evict_relation() is intended to be used for shared buffers only"}
			return
		}
		identity, eligible := logicalBufferIdentity(auth.Access().Catalog, target)
		if !eligible { return }
		key = &identity
	}
	if n.Kind == functions.EvictBuffer {
		definition, _ := e.Settings.Lookup("shared_buffers")
		capacity := int64(definition.DefaultValue().Number)
		if input < 1 || input > capacity {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf("bad buffer ID: %d", input)}
			return
		}
	}
	// Admission may have preceded an engine-mutex wait. Fence cancellation
	// under the final engine lock before any nontransactional cache change.
	if err = host.Operation().Context().Err(); err != nil { return }
	e.reconcileLogicalBuffersLocked()
	cache := e.logicalBuffersLocked()
	if n.Kind == functions.EvictBuffer {
		if cache.evictSlot(input) { result[0] = 1 }
	} else {
		result[0], result[2] = cache.evictMatching(key)
	}
	// Logical read entries are clean. No physical dirty/writeback is invented.
	return
}
