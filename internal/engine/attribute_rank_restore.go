package engine

import (
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Restore alone calls this interpreter, against its isolated catalog. It is not
// an UPDATE execution path and never grants writable access to catalog sources.
func restoreAttributeRank(e *core.Engine, op *session.Operation, raw *parser.Update) error {
	name, column, rank, err := table.RestoreAttributeRankSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return core.SQLError("42501", "permission denied for table pg_attribute")
	}
	e.Mu.Lock()
	if e.ImportTargets == nil || auth.Owner != &e.Catalog.Owner || auth.Session != op.Session || auth.RoleRecord != e.RoleStore.Published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.Catalog.Domain || auth.Catalog.State() != e.Catalog.State || auth.Catalog.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	ref, err := e.ResolveRelationReference(auth, name, false)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	next, err := table.AttributeRankCandidate(auth.Catalog, ref, column, rank)
	if err != nil || next == nil {
		e.Mu.Unlock()
		return err
	}
	return publishRestoreRepairLocked(e, op, auth, next, ref.Epoch)
}
