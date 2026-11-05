package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// aggregate takes only the leaf mutex, visits occupied slots, and returns a
// detached value. It never allocates, copies page snapshots or walks capacity.
func (c *logicalBufferCache) aggregate() functions.PgBuffercacheCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := functions.PgBuffercacheCounts{Capacity: c.capacity, Used: int64(len(c.bySlot))}
	result.Buckets[0].Buffers = result.Capacity - result.Used
	for _, entry := range c.bySlot {
		bucket := &result.Buckets[entry.usage]
		bucket.Buffers++
		result.UsageTotal += entry.usage
		if len(entry.owners) != 0 {
			result.Pinned++
			bucket.Pinned++
		}
	}
	return result
}

// Engine/session authority precedes the cache leaf. Revalidate after acquiring
// current authority, reconcile genuine cluster owners, then detach only counts.
func (h *contribHost) PgBuffercacheAggregate(n plan.InstalledPgBuffercacheAggregateExpr) (result functions.PgBuffercacheCounts, err error) {
	host := h.host()
	if err = host.Active(); err != nil { return }
	host.ReacquireEngine()
	defer host.ReleaseEngineLocked()
	auth, err := host.RoutineAuthorizationLocked()
	if err != nil { return }
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		err = ids.ErrCatalogReplaced
		return
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) { err = ids.ErrCatalogReplaced; return }
	if err = extension.ValidatePgBuffercacheIdentity(view.Catalog, current, n.Ref); err != nil { return }
	boundary := n.PgBuffercacheAggregate
	boundary.RoutineReference = current
	if err = extension.ValidatePgBuffercacheAggregateCall(view.Catalog, current, boundary); err != nil { return }
	if err = auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return }
	if err = auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true}); err != nil { return }
	if err = host.Operation().Context().Err(); err != nil { return }
	e := host.scope().Engine()
	if err = e.registeredLogicalBufferOwnerLocked(); err != nil { return }
	e.reconcileLogicalBuffersLocked()
	result = e.logicalBuffersLocked().aggregate()
	return
}
