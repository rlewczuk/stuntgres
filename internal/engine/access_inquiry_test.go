package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
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
			n, ok := q.Root.(*projectNode).expressions[0].(accessInquiryExpression)
			if !ok || n.count != len(tc.ids) || n.rowSecurity != tc.rls || n.ResultUsage().Type.ID() != sqltype.Bool || n.Properties().Volatility != Stable {
				t.Fatalf("typed signature %#v", n)
			}
			for i, id := range tc.ids {
				if n.args[i].ResultUsage().Type.ID() != id {
					t.Fatalf("argument %d got %v want %v", i, n.args[i].ResultUsage(), id)
				}
			}
			if err := n.validateStored(); err != nil {
				t.Fatal(err)
			}
			bad := n
			bad.properties.Volatility = Immutable
			if bad.validateStored() == nil {
				t.Fatal("accepted immutable inquiry")
			}
			bad = n
			bad.args[0] = nil
			if bad.validateStored() == nil {
				t.Fatal("accepted missing argument")
			}
		})
	}
}

func TestAccessInquiryPrivileges(t *testing.T) {
	for _, tc := range []struct {
		text string
		want rolePrivilege
	}{
		{"member", roleMember}, {"usage,set", roleUsage | roleSet}, {"MEMBER,MEMBER", roleMember},
		{"\t\n\v\f\r SeT \r", roleSet}, {"USAGE WITH ADMIN OPTION", roleAdmin},
		{"SET WITH GRANT OPTION,MEMBER", roleAdmin | roleMember},
	} {
		got, err := parseRolePrivileges(tc.text)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %v %v want %v", tc.text, got, err, tc.want)
		}
	}
	for _, text := range []string{"", "MEMBER,", "MEMBER,,SET", "MEMBER,bAd", "ADMIN", "MEMBER  WITH ADMIN OPTION", "MEMBER\tWITH ADMIN OPTION", "\u00a0MEMBER", "ſET"} {
		mask, err := parseRolePrivileges(text)
		var d *sqlstate.Error
		if mask != 0 || !errors.As(err, &d) || d.Code != "22023" || d.Position != "" {
			t.Fatalf("invalid token accepted: %q %v %v", text, mask, err)
		}
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
	actor, _ := auth.roles.Lookup("ar_unit_actor")
	target, _ := auth.roles.Lookup("ar_unit_target")
	for _, tc := range []struct {
		a, b uint64
		mask rolePrivilege
		want bool
	}{
		{actor.Ref.ID(), target.Ref.ID(), roleMember, true},
		{actor.Ref.ID(), target.Ref.ID(), roleUsage, false},
		{actor.Ref.ID(), target.Ref.ID(), roleSet, false},
		{actor.Ref.ID(), target.Ref.ID(), roleAdmin, true},
		{actor.Ref.ID(), actor.Ref.ID(), roleMember | roleUsage | roleSet, true},
		{actor.Ref.ID(), actor.Ref.ID(), roleAdmin, false},
		{0, 0, roleMember, true}, {0, 0, roleAdmin, false},
		{4294967295, 4294967295, roleSet, true},
		{0, 4294967295, roleMember, false},
		{actor.Ref.ID(), 4294967295, roleMember, false},
		{auth.state.CurrentUser.ID(), 4294967295, roleAdmin, true},
	} {
		got, err := accessRoleCheck(context.Background(), auth.roles, &e.authorizationDomain, tc.a, tc.b, tc.mask)
		if err != nil || got != tc.want {
			t.Fatalf("raw boundary %+v: %v %v", tc, got, err)
		}
	}
	// SQL shortcuts do not weaken any trusted authority entry point.
	foreign := authorization.Domain{}
	for _, ref := range []authorization.PrincipalRef{e.authorizationDomain.Principal(0), foreign.Principal(actor.Ref.ID()), e.authorizationDomain.Principal(4294967295)} {
		if _, err := auth.roles.InheritsRole(ref, ref); err == nil {
			t.Fatal("trusted inheritance accepted invalid self")
		}
		if _, err := auth.roles.CanSetRole(ref, ref); err == nil {
			t.Fatal("trusted SET accepted invalid self")
		}
		if _, err := auth.roles.AdminOfRole(ref, ref); err == nil {
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
	owner, _ := auth.roles.Lookup("ar_unit_owner")
	bypass, _ := auth.roles.Lookup("ar_unit_bypass")
	reader, _ := auth.roles.Lookup("ar_unit_reader")
	next := *d
	next.owner = owner.Ref
	using, err := policyUnitBind(t, e, op, d, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		for _, forced := range []bool{false, true} {
			next.rlsEnabled, next.rlsForced = enabled, forced
			for _, actor := range []authorization.PrincipalRef{owner.Ref, reader.Ref, bypass.Ref, auth.state.CurrentUser} {
				want := enabled && forced && actor != bypass.Ref && actor != auth.state.CurrentUser
				// A policy's provider may be irrelevant or even stale; eligibility must
				// never traverse its roles or demand expression enforcement.
				next.policies = []policyDefinition{{permissive: true, command: policyAll, roles: []policyRole{{public: true}}, using: using}}
				active, err := rowSecurityApplies(auth, &next, actor)
				if err != nil || active != want {
					t.Fatalf("eligibility %v/%v/%v: %v %v", enabled, forced, actor, active, err)
				}
				disposition, err := rowSecurityDecision(auth, &next, actor, policySelect, false)
				if err != nil || (disposition != rlsUnrestricted) != active {
					t.Fatal("enforcement eligibility diverged", disposition, active, err)
				}
				next.policies[0].roles = []policyRole{{ref: e.authorizationDomain.Principal(4294967295)}}
				active, err = rowSecurityApplies(auth, &next, actor)
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
	n := accessInquiryExpression{expressionInfo: expressionInfo{usage: boolean.Usage(), properties: ExprProperties{Volatility: Stable}}, count: 3}
	n.args[0] = literalExpression{expressionInfo: expressionInfo{usage: name.Usage()}, value: sqltype.String(name, "postgres")}
	v, _ := sqltype.OIDValue(oid, 10)
	n.args[1] = literalExpression{expressionInfo: expressionInfo{usage: oid.Usage()}, value: v}
	n.args[2] = literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.String(text, "MEMBER")}
	destination := New().CatalogRead()
	mapped, changed, err := retagBoundTypes(destination, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		typ, _ := destination.LookupType(d.ID())
		return typ
	})
	if err != nil || !changed {
		t.Fatal("did not remint recipe", mapped, changed, err)
	}
	fresh := mapped.(accessInquiryExpression)
	if fresh.ResultUsage().Type == n.ResultUsage().Type {
		t.Fatal("result retained foreign type")
	}
	for i, arg := range fresh.arguments() {
		if arg.ResultUsage().Type == n.args[i].ResultUsage().Type {
			t.Fatal("child retained foreign type", i)
		}
	}
	if err := fresh.validateStored(); err != nil {
		t.Fatal(err)
	}
	if n.args[0].ResultUsage().Type != name {
		t.Fatal("mutated original children")
	}
	rewritten, changed, err := mapBoundExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, func(child BoundExpr) (BoundExpr, bool, error) {
		if literal, ok := child.(literalExpression); ok && literal.value.Type().ID() == sqltype.Name {
			literal.value = sqltype.String(name, "replacement")
			return literal, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed {
		t.Fatal("nested leaf rewrite failed", changed, err)
	}
	got, _ := rewritten.(accessInquiryExpression).args[0].(literalExpression).value.Text()
	old, _ := n.args[0].(literalExpression).value.Text()
	if got != "replacement" || old != "postgres" {
		t.Fatal("recipe rewrite aliasing", got, old)
	}
	remapped, err := remapBoundExpression(n, func(ref ObjectRef) ObjectRef { return ref }, nil)
	if err != nil || remapped.(accessInquiryExpression).validateStored() != nil {
		t.Fatal("stored recipe remap", err)
	}
}
