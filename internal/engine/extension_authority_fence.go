package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// extensionAuthorityFence is the journal fence for one extension script's
// role, namespace and database observation.
type extensionAuthorityFence struct {
	observation extension.AuthorityObservation
}

func (extensionAuthorityFence) Kind() session.FenceKind { return fenceExtensionAuthority }

// Extension scripts insert the database-wide row before their members.
// Preserve that public uniqueness diagnostic when a competing installation
// wins after this script's private snapshot; unrelated catalog fences retain
// the central publication-conflict contract. The name check does not depend
// on the observation, so repeating it per fence keeps the first error.
func (f extensionAuthorityFence) currentLocked(e *Engine, c *session.Journal) error {
	for key, original := range c.Names {
		if key.ClassID == ids.ExtensionClassID && original == (ids.ObjectAddress{}) && e.catalog.state.NameAddress(key) != original {
			return extension.NameConflict(key.Name)
		}
	}
	o := f.observation
	if e.roles.published.Revision != o.Roles || o.Namespace == nil || o.Database == nil ||
		e.catalog.state.Get(o.Namespace.Address()) != o.Namespace ||
		e.catalog.state.Get(o.Database.Address()) != o.Database {
		return catalog.Conflict{}
	}
	return nil
}
