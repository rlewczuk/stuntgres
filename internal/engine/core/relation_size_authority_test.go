package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRelationSizeInvocationAuthority(t *testing.T) {
	e, relation := relationSizeOwnerFixture(t)
	aclUnitRoles(e, "rs_invoker", "rs_rule", "rs_ri", "rs_restricted")
	roles := e.Roles()
	invoker, _ := roles.Lookup("rs_invoker")
	rule, _ := roles.Lookup("rs_rule")
	ri, _ := roles.Lookup("rs_ri")
	restricted, _ := roles.Lookup("rs_restricted")
	bootstrap := roles.LookupRef("postgres")
	parent := *relation
	parent.Owner = restricted.Ref
	parent.ObjectRevision++
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	state.SetDescriptor(parent.Address(), &parent)
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()
	scope := tablespaceInfoUnitScope(t, e)
	leave, err := core.ScopeEnterRoutineEnvironment(scope, &catalog.Procedure{Owner: invoker.Ref, SecurityDefiner: true})
	if err != nil { t.Fatal(err) }
	defer func() { if err := leave(nil); err != nil { t.Error(err) } }()
	e.Mu.Lock()
	caller, err := scope.RoutineAuthorizationLocked()
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	ruleAuth := core.AuthorizationActingAs(caller, rule.Ref)
	riAuth := core.AuthorizationActingAs(caller, ri.Ref)
	scratch := core.ScopeRuleScratch(scope)
	scratch.RuleAction.Auth = &ruleAuth
	core.ScopeInstallRuleScratch(scope, scratch)
	beforeRole := scope.Op.RoleState()
	host := core.HostOf(scope)
	nameType := enginetest.Usage(e, sqltype.Name).Type
	resultType := enginetest.Usage(e, sqltype.Int8).Type
	database := e.CatalogRead().MustDatabase(e.Catalog.DatabaseAddress())
	for _, target := range []struct {
		kind functions.SizeKind
		address ids.ObjectAddress
		privilege authorization.Privileges
		name, label string
	}{
		{functions.DatabaseSize, database.Address(), authorization.ConnectPrivilege, database.Name(), "database"},
		{functions.TablespaceSize, ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.GlobalTablespaceOID}, authorization.CreatePrivilege, "pg_global", "tablespace"},
	} {
		for _, grant := range []authorization.PrincipalRef{invoker.Ref, rule.Ref, ri.Ref, restricted.Ref} {
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: grant}, Privileges: target.privilege}}}
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), target.address)
			if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil { t.Fatal(err) }
			check := func(want bool) {
				t.Helper()
				value, err := host.SizeInquiry(target.kind, sqltype.String(nameType, target.name), "main", resultType)
				if want {
					if err != nil { t.Fatal("invocation actor refused", target.name, grant, err) }
					if n, err := value.Integer(); err != nil || n < 0 { t.Fatal(value, err) }
					return
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for " + target.label + " " + target.name {
					t.Fatal("enclosing action substituted for invocation actor", target.name, grant, err)
				}
			}
			// Rule relation-write authority never substitutes for the live
			// effective invoker of a security-invoker size routine.
			scope.ForeignActionAuth = nil
			check(grant == invoker.Ref)
			// A genuine RI actor remains valid outside a restricted frame.
			scope.ForeignActionAuth = &riAuth
			check(grant == ri.Ref)
			// Enter the actual restricted-owner boundary with both enclosing
			// action actors installed; only the restricted owner may authorize.
			if err := scope.WithAmcheckOwner(&parent, func(*exec.Context) error {
				check(grant == restricted.Ref)
				return nil
			}); err != nil { t.Fatal(err) }
			if scope.Op.Restricted != nil || scope.Op.RoleState() != beforeRole || scope.ForeignActionAuth != &riAuth || core.ScopeRuleScratch(scope).RuleAction.Auth != &ruleAuth {
				t.Fatal("size inquiry changed enclosing authority lifetime")
			}
		}
	}
}
