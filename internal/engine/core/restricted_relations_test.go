package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestRestrictedRelationKindEffectiveSettings(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	const name = "restrict_nonsystem_relation_kind"
	set := func(s *session.SettingsStore, flags settings.RestrictedRelationKinds) {
		s.Mu.Lock()
		s.Typed[name] = settings.Value{Text: "view", RestrictedRelations: flags}
		s.Mu.Unlock()
	}
	set(op.EffectiveSettings(), settings.RestrictView)
	op.Restricted = &session.RestrictedInvocation{Owner: op.Session.Authorization.State.CurrentUser}
	image := op.EffectiveSettings()
	set(op.Session.Settings, settings.RestrictForeignTable)
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if auth.RestrictedRelations() != settings.RestrictView {
		t.Fatal("live setting replaced detached invocation policy")
	}
	owner := auth.AsUser(authorization.PrincipalRef{}).(core.AuthorizationView)
	if owner.RestrictedRelations() != settings.RestrictView {
		t.Fatal("view owner changed policy source")
	}
	set(image, settings.RestrictView|settings.RestrictForeignTable)
	if auth.RestrictedRelations() != settings.RestrictView|settings.RestrictForeignTable || owner.RestrictedRelations() != auth.RestrictedRelations() {
		t.Fatal("effective policy was cached or owner-specific")
	}
	op.Restricted = nil
	live, err := core.OperationAuthorization(op)
	if err != nil || live.RestrictedRelations() != settings.RestrictForeignTable {
		t.Fatal("next operation did not read live settings", err)
	}
}
