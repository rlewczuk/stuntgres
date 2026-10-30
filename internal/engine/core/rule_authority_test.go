package core_test

import (
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
)

type foreignRuleAuthority struct{}

func (foreignRuleAuthority) Access() access.View                                    { return access.View{} }
func (f foreignRuleAuthority) AsUser(authorization.PrincipalRef) view.RuleAuthority { return f }
func (foreignRuleAuthority) StorageLocked(ids.ObjectAddress) *storage.Relation      { return nil }
func (foreignRuleAuthority) RestrictedRelations() settings.RestrictedRelationKinds  { return 0 }
func (foreignRuleAuthority) RuleReplica() bool                                      { return false }

func TestRuleAuthorityAsUserAndRecovery(t *testing.T) {
	domain := new(authorization.Domain)
	caller, owner := domain.Principal(10), domain.Principal(20)
	var original core.AuthorizationView
	original.State.CurrentUser = caller
	core.SetAuthorizationPathPrefix(&original, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 99})

	switched := original.AsUser(owner)
	if got := switched.Access().State.CurrentUser; got != owner {
		t.Fatalf("AsUser current user = %v, want %v", got, owner)
	}
	if original.State.CurrentUser != caller {
		t.Fatal("AsUser changed the caller's authority")
	}
	recovered := core.RuleAuthorizationOf(switched)
	if recovered.State.CurrentUser != owner || core.AuthorizationPathPrefix(&recovered) != core.AuthorizationPathPrefix(&original) {
		t.Fatal("ruleAuthorizationOf lost authorization view fields")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("ruleAuthorizationOf accepted a foreign rule authority")
		}
	}()
	core.RuleAuthorizationOf(foreignRuleAuthority{})
}
