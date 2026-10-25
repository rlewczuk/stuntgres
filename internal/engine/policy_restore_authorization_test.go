package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"testing"
)

// SQL creates the role/session fixture; assertions exercise the real locked
// authorization capture and shared policy resolver, not public SQL responses.
func TestRLSPolicyRestoreAuthorization(t *testing.T) {
	source := New()
	sourceOp, sourceDone := roleOperation(t, source, "postgres")
	defer sourceDone()
	roleExecute(t, source, sourceOp, "CREATE ROLE selector_owner LOGIN")
	roleExecute(t, source, sourceOp, "CREATE ROLE next_owner LOGIN")
	stage := source.RestoreStage()
	op, done := roleOperation(t, stage, "postgres")
	defer done()
	roleExecute(t, stage, op, "SET SESSION AUTHORIZATION selector_owner")
	creation := statement(t, "CREATE POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER").(*parser.CreatePolicy)
	capture := func() AuthorizationView {
		t.Helper()
		stage.Mu.Lock()
		defer stage.Mu.Unlock()
		auth, err := stage.StatementAuthorizationLocked(op, creation)
		if err != nil {
			t.Fatal(err)
		}
		return auth
	}
	assertTargets := func(auth AuthorizationView, want authorization.PrincipalRef) {
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
	OperationReplay(op).TableDestination = true
	trusted := capture()
	if trusted.State.CurrentUser != ordinary.State.Authenticated || trusted.SessionRoleState != ordinary.State || op.RoleState() != ordinary.State {
		t.Fatal("trusted capture did not separate authority and session semantics")
	}
	assertTargets(trusted, ordinary.State.CurrentUser)
	// A fresh execution capture must observe a subsequent real session change;
	// the immutable earlier preflight snapshot must not change retroactively.
	OperationReplay(op).TableDestination = false
	roleExecute(t, stage, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, stage, op, "SET SESSION AUTHORIZATION next_owner")
	OperationReplay(op).TableDestination = true
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
	OperationReplay(sourceOp).TableDestination = true
	source.Mu.Lock()
	_, err := source.StatementAuthorizationLocked(sourceOp, creation)
	source.Mu.Unlock()
	if err == nil {
		t.Fatal("live-engine importer flag forged trusted policy authority")
	}
}
