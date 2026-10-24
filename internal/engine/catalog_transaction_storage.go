package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

type catalogAuthority struct {
	session              *session.Session
	authorization, roles uint64
	// Only selected ACL/default stages may survive revision-only peer ACL work.
	// Other statement authority retains its exact numeric revision fence.
	aclRoles, aclPublishedRoles            *access.RoleRecord
	ref                                    ids.ObjectRef
	descriptor                             catalog.Descriptor
	databaseAuthority, databasePredecessor catalog.Descriptor
	// Immutable consulted providers captured by the statement's catalog stage.
	observations []catalog.Descriptor
}

// Caller holds Engine.mu; never derive entry authority from a later live view.
func (v AuthorizationView) catalogAuthorityLocked(ref ids.ObjectRef, descriptor catalog.Descriptor) *catalogAuthority {
	return &catalogAuthority{session: v.session, authorization: v.Revision, roles: v.RoleRecord.Revision, ref: ref, descriptor: descriptor, databaseAuthority: v.Catalog.Get(v.Owner.DatabaseAddress()), databasePredecessor: v.DatabasePredecessor}
}

// Statement authority expires at successful Finish, never at outer Commit.
// Caller holds Engine.mu and the authority session's mutex. Catalog changes
// retain the owning transaction's validated installed predecessors.
func (e *Engine) catalogAuthorityPreflightLocked(a *catalogAuthority, c *session.Journal) error {
	if a == nil {
		return nil
	}
	if a.session.Authorization.Revision != a.authorization || e.catalog.state.Get(a.ref.Address) != a.descriptor {
		return catalog.Conflict{}
	}
	roles := e.sessionRolesLocked(a.session)
	if a.aclRoles != nil {
		if !roles.SameAuthority(a.aclRoles) || !e.roles.published.SameAuthority(a.aclPublishedRoles) {
			return catalog.Conflict{}
		}
	} else if roles.Revision != a.roles {
		return catalog.Conflict{}
	}
	if a.databasePredecessor != nil {
		if e.catalog.state.Get(e.catalog.DatabaseAddress()) != a.databasePredecessor {
			return catalog.Conflict{}
		}
		if c != nil {
			if original, ok := c.Originals[e.catalog.DatabaseAddress()]; ok && original != a.databasePredecessor {
				return catalog.Conflict{}
			}
			c.Originals[e.catalog.DatabaseAddress()] = a.databasePredecessor
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
	return session.PrivateStorage(&e.storage, c, a)
}
