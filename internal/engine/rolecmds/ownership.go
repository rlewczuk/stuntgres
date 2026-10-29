package rolecmds

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// ExecuteOwnership runs ALTER ... OWNER TO for types, domains, databases and large objects,
// and hands the relation kinds to core.ExecuteRelationOwnership.
func ExecuteOwnership(e *core.Engine, scope *core.StatementScope, s *parser.AlterOwner, count int) (core.Result, error) {
	name, identity := access.OwnershipRoleOperand(s.Target)
	if s.Kind == "database" || s.Kind == "large object" {
		return executeUnqualifiedOwnership(e, scope, s, name, identity, count)
	}
	if s.Kind != "type" && s.Kind != "domain" {
		return core.ExecuteRelationOwnership(e, scope, s.Name, name, identity, s.Kind, s.IfExists, count)
	}
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	change, err := role.PlanTypeOwnership(RoleHost{core.DDLHost{E: e, Op: scope.Op}}, auth, s, name, identity)
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		scope.RecordDDLCommand("ALTER "+strings.ToUpper(s.Kind), change.Address)
		e.Mu.Unlock()
		scope.CompleteUtility()
		return core.Result{}, scope.Op.Context().Err()
	}
	stage := core.OwnershipStage(e, auth, scope.Op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.Mu.Unlock()
	if err == nil {
		for _, a := range access.OwnershipRelationLocks(stage.Base, []ids.ObjectAddress{change.Object.Address()}, nil) {
			if err = scope.AcquireSequence(e.Catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
				break
			}
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	stage.RecordDDLCommand("ALTER "+strings.ToUpper(s.Kind), change.Object.Address())
	return core.PublishOwnership(e, scope, stage)
}

// Neither family has a namespace or a relation identity to lock.
func executeUnqualifiedOwnership(e *core.Engine, scope *core.StatementScope, s *parser.AlterOwner, name string, identity bool, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return fail(err)
	}
	var change role.OwnerChange
	if s.Kind == "large object" {
		change, err = role.PlanLargeObjectOwnership(RoleHost{core.DDLHost{E: e, Op: scope.Op}}, auth, s, name, identity)
	} else {
		change, err = role.PlanDatabaseOwnership(auth.View, auth, s, name, identity)
	}
	if err != nil {
		return fail(err)
	}
	if change.Object == nil {
		if s.Kind != "database" {
			scope.RecordDDLCommand("ALTER "+strings.ToUpper(s.Kind), change.Address)
		}
		e.Mu.Unlock()
		scope.CompleteUtility()
		return core.Result{}, scope.Op.Context().Err()
	}
	stage := core.OwnershipStage(e, auth, scope.Op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.Mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if s.Kind != "database" {
		stage.RecordDDLCommand("ALTER "+strings.ToUpper(s.Kind), change.Object.Address())
	}
	return core.PublishOwnership(e, scope, stage)
}
