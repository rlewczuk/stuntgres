package schemacmds

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/session"
)

// extensionAuthorityFence is the journal fence for one extension script's
// role, namespace and database observation.
type extensionAuthorityFence struct {
	observation extension.AuthorityObservation
}

func (extensionAuthorityFence) Kind() session.FenceKind { return core.FenceExtensionAuthority }

func (f extensionAuthorityFence) CurrentLocked(e *core.Engine, c *session.Journal) error {
	return f.observation.Current(c.Names, e.Catalog.State, e.RoleStore.Published.Revision)
}
