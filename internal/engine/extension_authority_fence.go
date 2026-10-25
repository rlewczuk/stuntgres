package engine

import (
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
)

// extensionAuthorityFence is the journal fence for one extension script's
// role, namespace and database observation.
type extensionAuthorityFence struct {
	observation extension.AuthorityObservation
}

func (extensionAuthorityFence) Kind() session.FenceKind { return FenceExtensionAuthority }

func (f extensionAuthorityFence) currentLocked(e *Engine, c *session.Journal) error {
	return f.observation.Current(c.Names, e.Catalog.State, e.RoleStore.Published.Revision)
}
