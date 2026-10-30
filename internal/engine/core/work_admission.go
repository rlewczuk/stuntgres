package core

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

// ActivateRuntime is called only by serial runtime owners after Engine.Mu and all
// state/store mutexes have been released, before opening providers or workers.
// Like foreignUnlocked, it hands off the replacement reservation before external
// admission or a closed pause, including requests with no workload policy.
// Captured inputs and owner/epoch publication fences remain authoritative.
// The physical transaction's command admission remains owned (busy-connection
// reentry is forbidden), but no state mutex spans the callback.
func (s *StatementScope) ActivateRuntime(ctx context.Context) context.Context {
	policy := workadmission.Request(ctx)
	if policy == nil && !util.HasExecutionPause(ctx) {
		return ctx
	}
	s.releaseCatalogReservation()
	ctx = util.ActivateExecutionPause(ctx)
	if policy == nil {
		return ctx
	}
	e, owner, ref := s.Engine(), s.Owner, s.TxRef
	return workadmission.ActivateRuntime(ctx, func(effective context.Context, class workadmission.Class) error {
		// Local workers have no row-lock/host-mutation routes. Their immutable
		// source/shape gate runs after the serial handoff, and the leader joins
		// all workers before resuming mutation. Do not touch scope lock state
		// from workers. Nested SQL owns and hands off its own scope.
		if !exec.InParallelWorker(effective) || effective.Value(aggregateInvocationKey{}) == s {
			s.releaseCatalogReservation()
		}
		if err := policy(effective, class); err != nil {
			return err
		}
		if err := s.Active(); err != nil {
			return err
		}
		owner.Mu.Lock()
		err := owner.Validate(ref)
		if err == nil {
			e.Mu.Lock()
			if ref.Epoch() != e.Catalog.State.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.Mu.Unlock()
		}
		owner.Mu.Unlock()
		return err
	})
}
