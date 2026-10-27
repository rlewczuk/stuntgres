package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
)

// Engine.mu protects committed and pending immutable role roots; statement
// admission prevents concurrent mutation of this session's transaction overlay.
func (e *Engine) OperationRolesLocked(op *session.Operation) *access.RoleRecord {
	if c := OperationCatalog(op); c != nil && c.Roles != nil {
		return c.Roles
	}
	if op != nil {
		return e.sessionRolesLocked(op.Session)
	}
	return e.RoleStore.Published
}

// OperationRoleSettingsLocked borrows defaults from the same complete record
// as the operation's principal authority. Caller holds Engine.mu.
func (e *Engine) OperationRoleSettingsLocked(op *session.Operation) *access.RoleSettings {
	return e.OperationRolesLocked(op).Settings
}

func (e *Engine) sessionRolesLocked(session *session.Session) *access.RoleRecord {
	if session != nil && session.Transactions != nil {
		if c := session.Transactions.Catalog; c != nil && c.Roles != nil {
			return c.Roles
		}
	}
	return e.RoleStore.Published
}

// The operation/session publication preflight has passed before this call.
func InstallOperationRolesLocked(e *Engine, op *session.Operation, next *access.RoleRecord) error {
	revision, err := e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		return err
	}
	root, membershipFrontier, err := next.Root.AssignMembershipOIDs(e.RoleStore.NextMembershipOID())
	if err != nil {
		return err
	}
	// The caller owns this fresh candidate; retained records are never mutated.
	next.Root = root
	next.Revision = revision
	e.RoleStore.InstallRevisionFrontierLocked(revision + 1)
	e.RoleStore.InstallMembershipFrontierLocked(membershipFrontier)
	t := op.Session.Transactions
	scope := ActiveScopeOfLease(op.Lease)
	if !t.RetainsTransaction() && t.State != session.TxActive && (scope == nil || scope.Catalog == nil) {
		e.RoleStore.PublishLocked(next)
		return nil
	}
	c := OperationCatalog(op).Clone()
	if c.Roles == nil {
		// Completed metadata and producer read fences do not supply this
		// first installed role root's write predecessor.
		c.RolesBase = e.OperationRolesLocked(op)
	}
	c.Roles = next
	installOperationCatalog(op, c)
	return nil
}

// Called only at final publication under Engine.mu, with the prepared combined
// catalog validated against the current committed root. Shared-role retirement
// must also reject dependencies in the other registered databases.
func (e *Engine) roleRetirementPreflightLocked(c *session.Journal, state *catalog.State) error {
	if err := role.RetirementPreflightLocked(c, &e.Catalog.Owner, &e.RoleStore.Domain, state); err != nil {
		return err
	}
	for ref := range c.RetiredRoles {
		if len((DDLHost{E: e}).OtherDatabaseDependencies(ref)) != 0 {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

// A publication/read refresh merges the existing immutable roots, never a
// statement replay or a second touched-object journal.
func (e *Engine) mergedJournalRolesLocked(ctx context.Context, c *session.Journal) (*access.RoleRecord, error) {
	if c == nil || c.Roles == nil {
		return e.RoleStore.Published, nil
	}
	if c.RolesBase == nil {
		return nil, catalog.Conflict{}
	}
	if c.Roles.SameAuthority(c.RolesBase) {
		return e.journalRolePublicationLocked(c, true)
	}
	root, err := authorization.MergeRoots(ctx, &c.RolesBase.Root, &c.Roles.Root, &e.RoleStore.Published.Root)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, catalog.Conflict{}
	}
	revision, err := e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		return nil, err
	}
	settings, err := access.MergeRoleSettings(ctx, c.RolesBase.Settings, c.Roles.Settings, e.RoleStore.Published.Settings)
	if err != nil {
		return nil, err
	}
	return &access.RoleRecord{Root: *root, Settings: settings, Revision: revision}, nil
}

func (e *Engine) refreshOperationRolesLocked(op *session.Operation) error {
	current := OperationCatalog(op)
	if current == nil || current.Roles == nil || current.RolesBase == e.RoleStore.Published {
		return nil
	}
	// A peer ACL publication changed no principal graph. Preserve the owner's
	// exact private root/base and logical ACL delta until final publication.
	if e.RoleStore.Published.SameAuthority(current.RolesBase) && e.RoleStore.Published.Settings == current.RolesBase.Settings {
		return nil
	}
	if current.Roles.SameAuthority(current.RolesBase) {
		// Graph-unchanged work still has an independent settings delta. Refresh
		// both roots while retaining the checked logical revision delta,
		// without reserving the allocator frontier during a read.
		if _, err := e.journalRolePublicationLocked(current, false); err != nil {
			return err
		}
		settings, err := access.MergeRoleSettings(op.Context(), current.RolesBase.Settings, current.Roles.Settings, e.RoleStore.Published.Settings)
		if err != nil {
			return err
		}
		c := current.Clone()
		next := e.RoleStore.Published.Advance()
		next.Settings = settings
		next.Revision = e.RoleStore.Published.Revision + (c.Roles.Revision - c.RolesBase.Revision)
		c.Roles, c.RolesBase = next, e.RoleStore.Published
		installOperationCatalog(op, c)
		return nil
	}
	next, err := e.mergedJournalRolesLocked(op.Context(), current)
	if err != nil {
		return err
	}
	c := current.Clone()
	c.Roles, c.RolesBase = next, e.RoleStore.Published
	e.RoleStore.AdvanceRevisionFrontierLocked(next.Revision)
	installOperationCatalog(op, c)
	return nil
}

func (e *Engine) publicationRolesLocked(ctx context.Context, c *session.Journal) (*session.Journal, error) {
	if c == nil || c.Roles == nil {
		return c, nil
	}
	if c.RolesBase != nil && c.Roles.SameAuthority(c.RolesBase) {
		// Only final installation mints the allocator-consistent ACL stamp.
		// Rebasing here would also let the actual-role merge frontier overwrite
		// the logical delta later in combined publication.
		if _, err := e.journalRolePublicationLocked(c, false); err != nil {
			return nil, err
		}
		return c, nil
	}
	if c.RolesBase == e.RoleStore.Published {
		return c, nil
	}
	next, err := e.mergedJournalRolesLocked(ctx, c)
	if err != nil {
		return nil, err
	}
	copy := *c
	copy.Roles, copy.RolesBase = next, e.RoleStore.Published
	return &copy, nil
}

// Graph-unchanged journals preserve their checked revision delta and merge
// settings independently into current authority. SameAuthority only supplies
// the zero principal delta, never complete-record equality or a publication
// fence. Actual-role journals retain the explicit graph-and-settings merge.
// materialize is false during validation, avoiding a new publication record.
func (e *Engine) journalRolePublicationLocked(c *session.Journal, materialize bool) (*access.RoleRecord, error) {
	if c == nil {
		return nil, nil
	}
	if c.Roles == nil {
		if c.RolesBase != nil && c.RolesBase != e.RoleStore.Published {
			return nil, catalog.Conflict{}
		}
		return nil, nil
	}
	base, next, current := c.RolesBase, c.Roles, e.RoleStore.Published
	if base == nil || current == nil {
		return nil, fmt.Errorf("stuntgres: missing authorization publication predecessor")
	}
	if next.Revision <= base.Revision {
		return nil, fmt.Errorf("stuntgres: invalid authorization revision delta")
	}
	if !next.SameAuthority(base) {
		if current == base {
			if materialize {
				return next, nil
			}
			return nil, nil
		}
		merged, err := e.mergedJournalRolesLocked(context.Background(), c)
		if err != nil || materialize {
			return merged, err
		}
		return nil, nil
	}
	if !current.SameAuthority(base) {
		// SameAuthority above proves that next has exactly base's principal
		// graph. Canonicalize that zero delta so MergeRoots takes its existing
		// candidate==base path instead of rescanning an unchanged graph.
		if _, err := authorization.MergeRoots(context.Background(), &base.Root, &base.Root, &current.Root); err != nil {
			return nil, catalog.Conflict{}
		}
	}
	settings, err := access.MergeRoleSettings(context.Background(), base.Settings, next.Settings, current.Settings)
	if err != nil {
		return nil, err
	}
	delta := next.Revision - base.Revision
	if current.Revision > ^uint64(0)-delta {
		return nil, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	revision, err := e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		return nil, err
	}
	if !materialize {
		return nil, nil
	}
	revision = max(revision, current.Revision+delta)
	if current == base && next.Revision == revision {
		return next, nil
	}
	published := current.Advance()
	published.Settings = settings
	published.Revision = revision
	return published, nil
}
