package session

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// CatalogAuthority is a statement's entry authority over the catalog: the
// session's authorization and role revisions, the object it resolved and the
// providers it consulted. It expires at the statement's successful Finish,
// never at the outer Commit.
type CatalogAuthority struct {
	Session              *Session
	Authorization, Roles uint64
	PublishedRoles       *access.RoleRecord
	// Only selected ACL/default stages may survive revision-only peer ACL work.
	// Other statements fence both the captured session revision and the exact
	// published root; provisional private revision numbers are not authority.
	ACLRoles, ACLPublishedRoles            *access.RoleRecord
	Ref                                    ids.ObjectRef
	Descriptor                             catalog.Descriptor
	DatabaseAuthority, DatabasePredecessor catalog.Descriptor
	// Immutable consulted providers captured by the statement's catalog stage.
	Observations []catalog.Descriptor
}

// PreflightLocked fences the authority against the committed catalog, the
// session's current role record roles and the published role record, with
// database the catalog's database address. A non-nil c retains the owning
// transaction's validated installed predecessors and records the database
// predecessor in c.Originals. The caller holds Engine.mu and the authority
// session's mutex; this takes no lock and returns catalog.Conflict.
func (a *CatalogAuthority) PreflightLocked(committed *catalog.State, roles, published *access.RoleRecord, database ids.ObjectAddress, c *Journal) error {
	if a.Session.Authorization.Revision != a.Authorization || committed.Get(a.Ref.Address) != a.Descriptor {
		return catalog.Conflict{}
	}
	if a.ACLRoles != nil {
		if !roles.SameAuthority(a.ACLRoles) || !published.SameAuthority(a.ACLPublishedRoles) {
			return catalog.Conflict{}
		}
	} else if roles.Revision != a.Roles || a.PublishedRoles != nil && published != a.PublishedRoles {
		return catalog.Conflict{}
	}
	if a.DatabasePredecessor != nil {
		if committed.Get(database) != a.DatabasePredecessor {
			return catalog.Conflict{}
		}
		if c != nil {
			if original, ok := c.Originals[database]; ok && original != a.DatabasePredecessor {
				return catalog.Conflict{}
			}
			c.Originals[database] = a.DatabasePredecessor
		}
	}
	for _, observed := range a.Observations {
		address := observed.Address()
		installed := committed.Get(address)
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
