package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
)

// Apply only to Restore's isolated catalog. Named target identities were
// captured before reading; final replacement fences their remapping again.
func (e *Engine) importExtensionOwners(ctx context.Context, op *session.Operation, records []extension.OwnerRecord) error {
	if len(records) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.importTargets == nil {
		return fmt.Errorf("stuntgres: invalid extension owner import authority")
	}
	candidate := catalog.CloneState(e.catalog.State)
	view := e.catalog.Read(candidate)
	roles := e.roles.read(&e.catalog.Owner, view, e.roles.published)
	ownersChanged, err := extension.ImportOwners(ctx, view, roles, records)
	if err != nil {
		return err
	}
	if !ownersChanged {
		return nil
	}
	// Ownership changes replace provider generations even when all SQL types are
	// unchanged. Refresh stored CHECK/view/policy/default captures through the
	// ordinary catalog expression remapper, not its type-only rename fast path.
	if err := stored.ReownExpressions(e.catalog.Read(candidate), candidate, nil, false, nil); err != nil {
		return err
	}
	e.catalog.PublishLocked(candidate)
	return nil
}
