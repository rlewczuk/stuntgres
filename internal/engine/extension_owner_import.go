package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
)

// ImportExtensionOwners applies the extension owner records of a native
// Restore.
//
// Apply only to Restore's isolated catalog. Named target identities were
// captured before reading; final replacement fences their remapping again.
func ImportExtensionOwners(e *core.Engine, ctx context.Context, op *session.Operation, records []extension.OwnerRecord) error {
	if len(records) == 0 {
		return nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if e.ImportTargets == nil {
		return fmt.Errorf("stuntgres: invalid extension owner import authority")
	}
	candidate := catalog.CloneState(e.Catalog.State)
	view := e.Catalog.Read(candidate)
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	ownersChanged, err := extension.ImportOwners(ctx, view, roles, records)
	if err != nil {
		return err
	}
	if !ownersChanged {
		return nil
	}
	// Owner/ACL changes replace provider generations even when all SQL types are
	// unchanged. Refresh stored CHECK/view/policy/default captures through the
	// ordinary catalog expression remapper, not its type-only rename fast path.
	if err := stored.ReownExpressions(e.Catalog.Read(candidate), candidate, nil, false, nil); err != nil {
		return err
	}
	e.Catalog.PublishLocked(candidate)
	return nil
}
