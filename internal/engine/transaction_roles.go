package engine

import (
	"context"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
)

// Engine.mu protects committed and pending immutable role roots; statement
// admission prevents concurrent mutation of this session's transaction overlay.
func (e *Engine) operationRolesLocked(op *session.Operation) *access.RoleRecord {
	if op != nil {
		return e.sessionRolesLocked(op.Session)
	}
	return e.roles.published
}
func (e *Engine) sessionRolesLocked(session *session.Session) *access.RoleRecord {
	if session != nil && session.Transactions != nil {
		if c := session.Transactions.Catalog; c != nil && c.Roles != nil {
			return c.Roles
		}
	}
	return e.roles.published
}

// The operation/session publication preflight has passed before this call.
func (e *Engine) installOperationRolesLocked(op *session.Operation, next *access.RoleRecord) error {
	revision, err := e.roles.revisionCandidateLocked()
	if err != nil {
		return err
	}
	// The caller owns this fresh candidate; retained records are never mutated.
	next.Revision = revision
	e.roles.nextRevision = revision + 1
	t := op.Session.Transactions
	if !t.RetainsTransaction() && t.State != session.TxActive {
		e.roles.published = next
		return nil
	}
	c := t.Catalog.Clone()
	if c.Roles == nil {
		// Completed metadata and producer read fences do not supply this
		// first installed role root's write predecessor.
		c.RolesBase = e.operationRolesLocked(op)
	}
	c.Roles = next
	t.Catalog = c
	return nil
}

// Session.mu is held, and all revision checks have reserved rollback capacity.
func touchOperationIdentityLocked(op *session.Operation) {
	t := op.Session.Transactions
	if (t.RetainsTransaction() || t.State == session.TxActive) && !t.IdentityCaptured {
		t.IdentityEntry = op.Session.Authorization.State
		t.IdentityCaptured = true
	}
}

// Called only at final publication under Engine.mu, with the prepared combined
// catalog validated against the current committed root. Shared-role retirement
// must also reject dependencies in the other registered databases.
func (e *Engine) roleRetirementPreflightLocked(c *session.Journal, state *catalog.State) error {
	if err := role.RetirementPreflightLocked(c, e.catalog.owner(), &e.roles.domain, state); err != nil {
		return err
	}
	for ref := range c.RetiredRoles {
		if len((ddlHost{e: e}).OtherDatabaseDependencies(ref)) != 0 {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

// A publication/read refresh merges the existing immutable roots, never a
// statement replay or a second touched-object journal.
func (e *Engine) mergedJournalRolesLocked(ctx context.Context, c *session.Journal) (*access.RoleRecord, error) {
	if c == nil || c.Roles == nil {
		return e.roles.published, nil
	}
	if c.RolesBase == nil {
		return nil, catalog.Conflict{}
	}
	root, err := authorization.MergeRoots(ctx, &c.RolesBase.Root, &c.Roles.Root, &e.roles.published.Root)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, catalog.Conflict{}
	}
	revision, err := e.roles.revisionCandidateLocked()
	if err != nil {
		return nil, err
	}
	return &access.RoleRecord{Root: *root, Revision: revision}, nil
}

func (e *Engine) refreshOperationRolesLocked(op *session.Operation) error {
	t := op.Session.Transactions
	if t == nil || t.Catalog == nil || t.Catalog.Roles == nil || t.Catalog.RolesBase == e.roles.published {
		return nil
	}
	next, err := e.mergedJournalRolesLocked(op.Context(), t.Catalog)
	if err != nil {
		return err
	}
	c := t.Catalog.Clone()
	c.Roles, c.RolesBase = next, e.roles.published
	e.roles.nextRevision = next.Revision + 1
	t.Catalog = c
	return nil
}

func (e *Engine) publicationRolesLocked(ctx context.Context, c *session.Journal) (*session.Journal, error) {
	if c == nil || c.Roles == nil {
		return c, nil
	}
	if c.RolesBase == e.roles.published {
		return c, nil
	}
	next, err := e.mergedJournalRolesLocked(ctx, c)
	if err != nil {
		return nil, err
	}
	copy := *c
	copy.Roles, copy.RolesBase = next, e.roles.published
	return &copy, nil
}
