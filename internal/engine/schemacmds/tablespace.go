package schemacmds

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecuteCreateTablespace runs CREATE TABLESPACE; ddl.CreateTablespace
// decides under Engine.Mu.
func ExecuteCreateTablespace(e *core.Engine, op *session.Operation, stmt *parser.CreateTablespace, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.Result{}, err
	}
	return core.Result{}, ddl.CreateTablespace(op.Context(), auth.View, stmt)
}

// ExecuteRenameTablespace publishes an immutable shared descriptor replacement.
func ExecuteRenameTablespace(e *core.Engine, scope *core.StatementScope, stmt *parser.AlterTablespaceRename, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	ref, err := ddl.TablespaceRef(auth.View, stmt.Name.Text)
	if err != nil {
		return fail(err)
	}
	if err := auth.View.Check(auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for tablespace " + stmt.Name.Text}
		}
		return fail(err)
	}
	if err := catalog.ValidateTablespaceName(stmt.NewName.Text, auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.TablespaceClassID, Name: stmt.NewName.Text}) != (ids.ObjectAddress{})); err != nil {
		return fail(err)
	}
	old := auth.Catalog.Get(ref.Address).(*catalog.Tablespace)
	stage := core.OwnershipStage(e, auth, scope.Op)
	next := *old
	next.ObjectName = stmt.NewName.Text
	next.ObjectRevision, err = stage.CascadeRevision(old.Revision())
	if err == nil {
		err = stage.Replace(&next)
	}
	e.Mu.Unlock()
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	return core.PublishOwnership(e, scope, stage)
}

// ExecuteDropTablespace handles the reachable native identities. Both fixed
// builtin OIDs remain pinned even after rename or transfer; physical ordinary
// tablespaces cannot be registered in embedded mode.
func ExecuteDropTablespace(e *core.Engine, scope *core.StatementScope, stmt *parser.DropTablespace, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	ref, err := ddl.TablespaceRef(auth.View, stmt.Name.Text)
	if err != nil {
		var diagnostic *sqlstate.Error
		if stmt.IfExists && errors.As(err, &diagnostic) && diagnostic.Code == "42704" {
			if err := scope.Op.Context().Err(); err != nil {
				return fail(err)
			}
			e.Mu.Unlock()
			scope.CompleteUtility()
			scope.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`tablespace "%s" does not exist, skipping`, stmt.Name.Text)})
			return core.Result{}, scope.Op.Context().Err()
		}
		return fail(err)
	}
	if err := auth.View.Check(auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for tablespace " + stmt.Name.Text})
}
