package viewcmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// SQL creates the role/session fixture; assertions exercise the real locked
// authorization capture and shared policy resolver, not public SQL responses.
func TestRLSPolicyRestoreAuthorization(t *testing.T) {
	source := engine.New()
	sourceOp, sourceDone := enginetest.RoleOperation(t, source, "postgres")
	defer sourceDone()
	enginetest.RoleExecute(t, source, sourceOp, "CREATE ROLE selector_owner LOGIN")
	enginetest.RoleExecute(t, source, sourceOp, "CREATE ROLE next_owner LOGIN")
	stage := source.RestoreStage()
	op, done := enginetest.RestoreOperation(t, stage, "postgres")
	defer done()
	enginetest.RoleExecute(t, stage, op, "SET SESSION AUTHORIZATION selector_owner")
	creation := enginetest.Statement(t, "CREATE POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER").(*parser.CreatePolicy)
	capture := func() core.AuthorizationView {
		t.Helper()
		stage.Mu.Lock()
		defer stage.Mu.Unlock()
		auth, err := stage.StatementAuthorizationLocked(op, creation)
		if err != nil {
			t.Fatal(err)
		}
		return auth
	}
	assertTargets := func(auth core.AuthorizationView, want authorization.PrincipalRef) {
		t.Helper()
		if err := view.PolicyPreflight(op, auth.View, creation, false); err != nil {
			t.Fatal("preflight", err)
		}
		roles, warning, err := view.ResolvePolicyRoles(auth.View, creation.Roles)
		if err != nil || warning || len(roles) != 3 {
			t.Fatal("selector vector", roles, warning, err)
		}
		for _, role := range roles {
			if role.Public || role.Ref != want {
				t.Fatal("trusted authority leaked into target", role, want)
			}
		}
	}
	ordinary := capture()
	if ordinary.State != ordinary.SessionRoleState || ordinary.SessionRoleState != op.RoleState() {
		t.Fatal("ordinary capture lost actual session state")
	}
	assertTargets(ordinary, ordinary.State.CurrentUser)
	core.OperationReplay(op).TableDestination = true
	trusted := capture()
	if trusted.State.CurrentUser != ordinary.State.Authenticated || trusted.SessionRoleState != ordinary.State || op.RoleState() != ordinary.State {
		t.Fatal("trusted capture did not separate authority and session semantics")
	}
	assertTargets(trusted, ordinary.State.CurrentUser)
	// A fresh execution capture must observe a subsequent real session change;
	// the immutable earlier preflight snapshot must not change retroactively.
	core.OperationReplay(op).TableDestination = false
	enginetest.RoleExecute(t, stage, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, stage, op, "SET SESSION AUTHORIZATION next_owner")
	core.OperationReplay(op).TableDestination = true
	fresh := capture()
	if fresh.Revision == trusted.Revision || fresh.SessionRoleState.CurrentUser == trusted.SessionRoleState.CurrentUser || fresh.State.CurrentUser != trusted.State.CurrentUser {
		t.Fatal("fresh capture reused old semantics or lost trusted authority")
	}
	assertTargets(fresh, op.RoleState().CurrentUser)
	assertTargets(trusted, ordinary.State.CurrentUser)
	if trusted.SessionRoleState != ordinary.State {
		t.Fatal("retained semantic snapshot mutated")
	}
	// The flag alone still grants no trusted authority on a live engine.
	core.OperationReplay(sourceOp).TableDestination = true
	source.Mu.Lock()
	_, err := source.StatementAuthorizationLocked(sourceOp, creation)
	source.Mu.Unlock()
	if err == nil {
		t.Fatal("live-engine importer flag forged trusted policy authority")
	}
}
