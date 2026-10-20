package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
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
	candidate := catalog.CloneState(e.catalog.state)
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(candidate), e.roles.published)
	ownersChanged, err := extension.ImportOwners(ctx, candidate, roles, records)
	if err != nil {
		return err
	}
	if !ownersChanged {
		return nil
	}
	// Ownership changes replace provider generations even when all SQL types are
	// unchanged. Refresh stored CHECK/view/policy/default captures through the
	// ordinary catalog expression remapper, not its type-only rename fast path.
	if err := e.reownCatalogExpressions(candidate, nil, false); err != nil {
		return err
	}
	e.catalog.state = candidate
	return nil
}
