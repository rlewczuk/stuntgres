package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/view"
)

func TestAccessInquiryRoleOIDs(t *testing.T) {
	e, op, done, _ := policyUnitFixture(t)
	defer done()
	for _, q := range []string{"CREATE ROLE ar_unit_actor", "CREATE ROLE ar_unit_middle", "CREATE ROLE ar_unit_target", "GRANT ar_unit_middle TO ar_unit_actor WITH INHERIT FALSE, SET TRUE", "GRANT ar_unit_target TO ar_unit_middle WITH ADMIN TRUE, INHERIT TRUE, SET FALSE"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := auth.Roles.Lookup("ar_unit_actor")
	target, _ := auth.Roles.Lookup("ar_unit_target")
	for _, tc := range []struct {
		a, b uint64
		mask authorization.RolePrivileges
		want bool
	}{
		{actor.Ref.ID(), target.Ref.ID(), authorization.RoleMember, true},
		{actor.Ref.ID(), target.Ref.ID(), authorization.RoleUsage, false},
		{actor.Ref.ID(), target.Ref.ID(), authorization.RoleSet, false},
		{actor.Ref.ID(), target.Ref.ID(), authorization.RoleAdmin, true},
		{actor.Ref.ID(), actor.Ref.ID(), authorization.RoleMember | authorization.RoleUsage | authorization.RoleSet, true},
		{actor.Ref.ID(), actor.Ref.ID(), authorization.RoleAdmin, false},
		{0, 0, authorization.RoleMember, true}, {0, 0, authorization.RoleAdmin, false},
		{4294967295, 4294967295, authorization.RoleSet, true},
		{0, 4294967295, authorization.RoleMember, false},
		{actor.Ref.ID(), 4294967295, authorization.RoleMember, false},
		{auth.State.CurrentUser.ID(), 4294967295, authorization.RoleAdmin, true},
	} {
		got, err := authorization.HasRole(context.Background(), auth.Roles, &e.RoleStore.Domain, tc.a, tc.b, tc.mask)
		if err != nil || got != tc.want {
			t.Fatalf("raw boundary %+v: %v %v", tc, got, err)
		}
	}
	// SQL shortcuts do not weaken any trusted authority entry point.
	foreign := authorization.Domain{}
	for _, ref := range []authorization.PrincipalRef{e.RoleStore.Domain.Principal(0), foreign.Principal(actor.Ref.ID()), e.RoleStore.Domain.Principal(4294967295)} {
		if _, err := auth.Roles.InheritsRole(ref, ref); err == nil {
			t.Fatal("trusted inheritance accepted invalid self")
		}
		if _, err := auth.Roles.CanSetRole(ref, ref); err == nil {
			t.Fatal("trusted SET accepted invalid self")
		}
		if _, err := auth.Roles.AdminOfRole(ref, ref); err == nil {
			t.Fatal("trusted ADMIN accepted invalid self")
		}
	}
}

func TestAccessInquiryRLSDecision(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	for _, q := range []string{"CREATE ROLE ar_unit_owner", "CREATE ROLE ar_unit_reader", "CREATE ROLE ar_unit_bypass BYPASSRLS", "GRANT ar_unit_owner TO ar_unit_reader WITH INHERIT TRUE"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := auth.Roles.Lookup("ar_unit_owner")
	bypass, _ := auth.Roles.Lookup("ar_unit_bypass")
	reader, _ := auth.Roles.Lookup("ar_unit_reader")
	next := *d
	next.Owner = owner.Ref
	using, err := policyUnitBind(t, e, op, d, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		for _, forced := range []bool{false, true} {
			next.Security.RLSEnabled, next.Security.RLSForced = enabled, forced
			for _, actor := range []authorization.PrincipalRef{owner.Ref, reader.Ref, bypass.Ref, auth.State.CurrentUser} {
				want := enabled && forced && actor != bypass.Ref && actor != auth.State.CurrentUser
				// A policy's provider may be irrelevant or even stale; eligibility must
				// never traverse its roles or demand expression enforcement.
				next.Security.Policies = []catalog.PolicyDefinition{{Permissive: true, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Public: true}}, Using: view.PolicyPayloadOf(using)}}
				active, err := access.RowSecurityApplies(auth.Roles, &next, actor)
				if err != nil || active != want {
					t.Fatalf("eligibility %v/%v/%v: %v %v", enabled, forced, actor, active, err)
				}
				selection, err := access.SelectRowSecurity(auth.Roles, &next, actor, catalog.PolicySelect, false, false)
				if err != nil || (selection.Count != 0) != active {
					t.Fatal("enforcement eligibility diverged", selection, active, err)
				}
				next.Security.Policies[0].Roles = []catalog.PolicyRole{{Ref: e.RoleStore.Domain.Principal(4294967295)}}
				active, err = access.RowSecurityApplies(auth.Roles, &next, actor)
				if err != nil || active != want {
					t.Fatal("inquiry inspected policy", active, err)
				}
			}
		}
	}
}
