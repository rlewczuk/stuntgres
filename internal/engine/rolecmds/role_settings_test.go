package rolecmds_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestRoleSettingsExecutorAtomicity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	boot, _ := e.Roles().Lookup("postgres")
	key := access.RoleSettingKey{Role: boot.Ref}
	execute := func(s *parser.AlterRoleSettings) error {
		t.Helper()
		_, err := rolecmds.ExecuteRoleSettings(e, &core.StatementScope{Op: op}, s, 0)
		return err
	}
	issuer := op.EffectiveSettings()
	issuer.Mu.Lock()
	liveApplicationName := issuer.Value("application_name")
	issuer.Mu.Unlock()
	set := func(name, value string) *parser.AlterRoleSettings {
		return &parser.AlterRoleSettings{Role: parser.RoleSpec{Name: "postgres"}, Set: &parser.Set{Name: parser.Identifier{Text: name}, Values: []string{value}}}
	}
	original := e.RoleStore.Published
	if err := execute(set("application_name", "first")); err != nil {
		t.Fatal(err)
	}
	first := e.RoleStore.Published
	firstRow := first.Settings.Lookup(key)
	if first == original || !first.SameAuthority(original) || len(firstRow) != 1 || firstRow[0] != (access.RoleSetting{Name: "application_name", Value: "first"}) {
		t.Fatal("settings-only publication lost graph lineage or owned row", firstRow)
	}
	if len(original.Settings.Lookup(key)) != 0 {
		t.Fatal("retained predecessor mutated")
	}
	if err := execute(set("search_path", "public")); err != nil {
		t.Fatal(err)
	}
	second := e.RoleStore.Published
	if err := execute(set("APPLICATION_NAME", "replacement")); err != nil {
		t.Fatal(err)
	}
	want := []access.RoleSetting{{Name: "application_name", Value: "replacement"}, {Name: "search_path", Value: "public"}}
	if got := e.RoleStore.Published.Settings.Lookup(key); !slices.Equal(got, want) {
		t.Fatalf("row=%v want=%v", got, want)
	}
	if first.Settings.Lookup(key)[0].Value != "first" || second.Settings.Lookup(key)[0].Value != "first" {
		t.Fatal("replacement mutated retained row backing")
	}
	before, revision := e.RoleStore.Published, e.RoleStore.NextRevision()
	if err := execute(set("statement_timeout", "not a timeout")); err == nil {
		t.Fatal("invalid persisted value accepted")
	}
	if e.RoleStore.Published != before || e.RoleStore.NextRevision() != revision {
		t.Fatal("rejected value changed complete record or allocator")
	}
	if err := execute(&parser.AlterRoleSettings{Role: parser.RoleSpec{Name: "postgres"}, Reset: &parser.Reset{Name: parser.Identifier{Text: "not_a_setting"}}}); err == nil {
		t.Fatal("unknown reset accepted with present exact row")
	}
	if e.RoleStore.Published != before {
		t.Fatal("failed reset published a prefix")
	}
	issuer.Mu.Lock()
	if issuer.Value("application_name") != liveApplicationName {
		issuer.Mu.Unlock()
		t.Fatal("persisted mutation changed issuer settings")
	}
	issuer.Mu.Unlock()
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.Ctx = ctx
	if err := execute(set("application_name", "canceled")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled mutation: %v", err)
	}
	if e.RoleStore.Published != before || e.RoleStore.NextRevision() != revision {
		t.Fatal("cancellation changed complete publication")
	}
}

func TestRoleSettingsTargetCurrentAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, op, "settings_actor", true, parser.RoleOptions{})
	roleAttributeCreate(t, e, op, "settings_target", true, parser.RoleOptions{})
	actorOp, actorDone := enginetest.RoleOperation(t, e, "settings_actor")
	defer actorDone()
	decide := func(s *parser.AlterRoleSettings) (role.RoleSettingsTarget, error) {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		view, err := e.AuthorizationLocked(actorOp)
		if err != nil {
			return role.RoleSettingsTarget{}, err
		}
		return role.PlanRoleSettingsTarget(view.View, s)
	}
	self, err := decide(&parser.AlterRoleSettings{Role: parser.RoleSpec{Identity: "current"}})
	if err != nil || self.Key.Role != actor.Ref {
		t.Fatal("execute-time current identity", self, err)
	}
	for _, s := range []*parser.AlterRoleSettings{
		{Role: parser.RoleSpec{Name: "settings_target"}},
		{All: true},
	} {
		_, err := decide(s)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
			t.Fatalf("authority error=%v", err)
		}
	}
	_, err = decide(&parser.AlterRoleSettings{Role: parser.RoleSpec{Name: "missing_settings_role"}, Database: &parser.Identifier{Text: "missing_settings_database"}})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" {
		t.Fatalf("role lookup must precede database lookup: %v", err)
	}
	roleAttributeApply(t, e, op, roleAttributeAlter("settings_actor", parser.RoleOptions{Present: parser.RoleCreateRole, CreateRole: true}))
	target, _ := e.Roles().Lookup("settings_target")
	boot, _ := e.Roles().Lookup("postgres")
	if err := grantMembership(t, e, authorization.Membership{Role: target.Ref, Member: actor.Ref, Grantor: boot.Ref, Admin: true, Inherit: true, Set: true}); err != nil {
		t.Fatal(err)
	}
	held := &parser.AlterRoleSettings{Role: parser.RoleSpec{Name: "settings_target"}, Set: &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{"held"}}}
	if _, err := decide(held); err != nil {
		t.Fatal("current CreateRole plus Admin denied", err)
	}
	admitted := e.RoleStore.Published
	roleAttributeApply(t, e, op, roleAttributeAlter("settings_actor", parser.RoleOptions{Present: parser.RoleCreateRole, CreateRole: false}))
	before := e.RoleStore.Published
	_, err = rolecmds.ExecuteRoleSettings(e, &core.StatementScope{Op: actorOp}, held, 0)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
		t.Fatalf("held authority was reused: %v", err)
	}
	if before == admitted || e.RoleStore.Published != before || len(before.Settings.Lookup(access.RoleSettingKey{Role: target.Ref})) != 0 {
		t.Fatal("revoked authority published settings or reused admitted root")
	}
}
