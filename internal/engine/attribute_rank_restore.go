package engine

import (
	"fmt"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Restore alone calls this interpreter, against its isolated catalog. It is not
// an UPDATE execution path and never grants writable access to catalog sources.
func (e *Engine) restoreAttributeRank(op *session.Operation, raw *parser.Update) error {
	name, column, rank, err := table.RestoreAttributeRankSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "permission denied for table pg_attribute")
	}
	e.mu.Lock()
	if e.importTargets == nil || auth.Owner != e.catalog.owner() || auth.session != op.Session || auth.RoleRecord != e.roles.published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.catalog.Domain || auth.Catalog.State() != e.catalog.state || auth.Catalog.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	next, err := table.AttributeRankCandidate(auth.Catalog, ref, column, rank)
	if err != nil || next == nil {
		e.mu.Unlock()
		return err
	}
	return e.publishRestoreRepairLocked(op, auth, next, ref.Epoch)
}
