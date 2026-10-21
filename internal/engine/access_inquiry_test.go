package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestAccessInquiryBinding(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	for _, tc := range []struct {
		sql string
		ids []sqltype.ID
		rls bool
	}{
		{"SELECT pg_has_role($1,$2,$3)", []sqltype.ID{sqltype.Name, sqltype.Name, sqltype.Text}, false},
		{"SELECT pg_has_role(10::oid,$1,$2)", []sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Text}, false},
		{"SELECT pg_has_role($1,10::oid,$2)", []sqltype.ID{sqltype.Name, sqltype.Oid, sqltype.Text}, false},
		{"SELECT pg_has_role(10::oid,10::oid,$1)", []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Text}, false},
		{"SELECT pg_has_role($1,$2)", []sqltype.ID{sqltype.Name, sqltype.Text}, false},
		{"SELECT pg_has_role(10::oid,$1)", []sqltype.ID{sqltype.Oid, sqltype.Text}, false},
		{"SELECT row_security_active($1)", []sqltype.ID{sqltype.Text}, true},
		{"SELECT row_security_active(0::oid)", []sqltype.ID{sqltype.Oid}, true},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			q := domainUnitBindSelect(t, e, op, tc.sql)
			n, ok := q.Root.(*plan.ProjectNode).Expressions[0].(functions.AccessInquiry)
			if !ok || n.Count != len(tc.ids) || n.RowSecurity != tc.rls || n.ResultUsage().Type.ID() != sqltype.Bool || n.Properties().Volatility != expr.Stable {
				t.Fatalf("typed signature %#v", n)
			}
			for i, id := range tc.ids {
				if n.Args[i].ResultUsage().Type.ID() != id {
					t.Fatalf("argument %d got %v want %v", i, n.Args[i].ResultUsage(), id)
				}
			}
			if err := n.Validate(); err != nil {
				t.Fatal(err)
			}
			bad := n
			bad.Property.Volatility = expr.Immutable
			if bad.Validate() == nil {
				t.Fatal("accepted immutable inquiry")
			}
			bad = n
			bad.Args[0] = nil
			if bad.Validate() == nil {
				t.Fatal("accepted missing argument")
			}
		})
	}
}

func TestAccessInquiryRoleOIDs(t *testing.T) {
	e, op, done, _ := policyUnitFixture(t)
	defer done()
	for _, q := range []string{"CREATE ROLE ar_unit_actor", "CREATE ROLE ar_unit_middle", "CREATE ROLE ar_unit_target", "GRANT ar_unit_middle TO ar_unit_actor WITH INHERIT FALSE, SET TRUE", "GRANT ar_unit_target TO ar_unit_middle WITH ADMIN TRUE, INHERIT TRUE, SET FALSE"} {
		roleExecute(t, e, op, q)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
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
		got, err := authorization.HasRole(context.Background(), auth.Roles, &e.roles.domain, tc.a, tc.b, tc.mask)
		if err != nil || got != tc.want {
			t.Fatalf("raw boundary %+v: %v %v", tc, got, err)
		}
	}
	// SQL shortcuts do not weaken any trusted authority entry point.
	foreign := authorization.Domain{}
	for _, ref := range []authorization.PrincipalRef{e.roles.domain.Principal(0), foreign.Principal(actor.Ref.ID()), e.roles.domain.Principal(4294967295)} {
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
		roleExecute(t, e, op, q)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
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
				next.Security.Policies = []catalog.PolicyDefinition{{Permissive: true, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Public: true}}, Using: policyPayloadOf(using)}}
				active, err := access.RowSecurityApplies(auth.Roles, &next, actor)
				if err != nil || active != want {
					t.Fatalf("eligibility %v/%v/%v: %v %v", enabled, forced, actor, active, err)
				}
				disposition, err := access.RowSecurityDecision(auth.Roles, &next, actor, catalog.PolicySelect, false)
				if err != nil || (disposition != access.RLSUnrestricted) != active {
					t.Fatal("enforcement eligibility diverged", disposition, active, err)
				}
				next.Security.Policies[0].Roles = []catalog.PolicyRole{{Ref: e.roles.domain.Principal(4294967295)}}
				active, err = access.RowSecurityApplies(auth.Roles, &next, actor)
				if err != nil || active != want {
					t.Fatal("inquiry inspected policy", active, err)
				}
			}
		}
	}
}

func TestAccessInquiryLifecycle(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	boolean, _ := view.LookupType(sqltype.Bool)
	oid, _ := view.LookupType(sqltype.Oid)
	name, _ := view.LookupType(sqltype.Name)
	text, _ := view.LookupType(sqltype.Text)
	n := functions.AccessInquiry{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Count: 3}
	n.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: name.Usage()}, Value: sqltype.String(name, "postgres")}
	v, _ := sqltype.OIDValue(oid, 10)
	n.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: v}
	n.Args[2] = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "MEMBER")}
	destination := New().CatalogRead()
	mapped, changed, err := RetagTypes(destination, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		typ, _ := destination.LookupType(d.ID())
		return typ
	})
	if err != nil || !changed {
		t.Fatal("did not remint recipe", mapped, changed, err)
	}
	fresh := mapped.(functions.AccessInquiry)
	if fresh.ResultUsage().Type == n.ResultUsage().Type {
		t.Fatal("result retained foreign type")
	}
	for i, arg := range fresh.Children() {
		if arg.ResultUsage().Type == n.Args[i].ResultUsage().Type {
			t.Fatal("child retained foreign type", i)
		}
	}
	if err := fresh.Validate(); err != nil {
		t.Fatal(err)
	}
	if n.Args[0].ResultUsage().Type != name {
		t.Fatal("mutated original children")
	}
	rewritten, changed, err := MapExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, func(child expr.Expr) (expr.Expr, bool, error) {
		if literal, ok := child.(plan.LiteralExpr); ok && literal.Value.Type().ID() == sqltype.Name {
			literal.Value = sqltype.String(name, "replacement")
			return literal, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed {
		t.Fatal("nested leaf rewrite failed", changed, err)
	}
	got, _ := rewritten.(functions.AccessInquiry).Args[0].(plan.LiteralExpr).Value.Text()
	old, _ := n.Args[0].(plan.LiteralExpr).Value.Text()
	if got != "replacement" || old != "postgres" {
		t.Fatal("recipe rewrite aliasing", got, old)
	}
	remapped, err := RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || remapped.(functions.AccessInquiry).Validate() != nil {
		t.Fatal("stored recipe remap", err)
	}
}
