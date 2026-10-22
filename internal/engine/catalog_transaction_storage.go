package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

type catalogAuthority struct {
	session                                *session.Session
	authorization, roles                   uint64
	ref                                    ids.ObjectRef
	descriptor                             catalog.Descriptor
	databaseAuthority, databasePredecessor catalog.Descriptor
	// Immutable consulted providers captured by the statement's catalog stage.
	observations []catalog.Descriptor
}

// Caller holds Engine.mu; never derive entry authority from a later live view.
func (v AuthorizationView) catalogAuthorityLocked(ref ids.ObjectRef, descriptor catalog.Descriptor) *catalogAuthority {
	return &catalogAuthority{session: v.session, authorization: v.Revision, roles: v.RoleRecord.Revision, ref: ref, descriptor: descriptor, databaseAuthority: v.Catalog.Get(catalog.CurrentDatabaseAddress), databasePredecessor: v.DatabasePredecessor}
}

// Statement authority expires at successful Finish, never at outer Commit.
// Caller holds Engine.mu and the authority session's mutex. Catalog changes
// retain the owning transaction's validated installed predecessors.
func (e *Engine) catalogAuthorityPreflightLocked(a *catalogAuthority, c *session.Journal) error {
	if a == nil {
		return nil
	}
	if a.session.Authorization.Revision != a.authorization || e.sessionRolesLocked(a.session).Revision != a.roles || e.catalog.state.Get(a.ref.Address) != a.descriptor {
		return catalog.Conflict{}
	}
	if a.databasePredecessor != nil {
		if e.catalog.state.Get(catalog.CurrentDatabaseAddress) != a.databasePredecessor {
			return catalog.Conflict{}
		}
		if c != nil {
			if original, ok := c.Originals[catalog.CurrentDatabaseAddress]; ok && original != a.databasePredecessor {
				return catalog.Conflict{}
			}
			c.Originals[catalog.CurrentDatabaseAddress] = a.databasePredecessor
		}
	}
	for _, observed := range a.observations {
		address := observed.Address()
		installed := e.catalog.state.Get(address)
		if installed == observed {
			continue
		}
		if c != nil {
			original, owned := c.Originals[address]
			// Only an exact owner-private provider may use its predecessor.
			// An installed observation must never be rescued by the overlay.
			if owned && original != observed && installed == original && c.Descriptors[address] == observed {
				continue
			}
		}
		return catalog.Conflict{}
	}
	return nil
}

// Callers hold Engine.mu; private storage is never installed before Finish/Commit.
func (e *Engine) privateStorageLocked(c *session.Journal, a ids.ObjectAddress) *storage.Relation {
	return privateStorage(&e.storage, c, a)
}

// privateStorage is a's storage as journal c sees it over the published
// store. Callers hold Engine.mu.
func privateStorage(published *storage.Store, c *session.Journal, a ids.ObjectAddress) *storage.Relation {
	if c != nil {
		if shape, ok := c.Storage[a]; ok {
			return shape.Replacement
		}
	}
	return published.Relations[a]
}
