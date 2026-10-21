package access

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

func TestDropOwnedPolicyTargets(t *testing.T) {
	principals := &authorization.Domain{}
	source, target := principals.Principal(100), principals.Principal(101)
	old := []catalog.PolicyDefinition{{Name: "mixed", Roles: []catalog.PolicyRole{{Ref: source}, {Ref: target}, {Ref: source}}}, {Name: "last", Roles: []catalog.PolicyRole{{Ref: source}, {Ref: source}}}, {Name: "public", Roles: []catalog.PolicyRole{{Public: true}, {Ref: source}}}}
	before := []int{len(old[0].Roles), len(old[1].Roles), len(old[2].Roles)}
	got, changed := OwnedPolicies(old, map[authorization.PrincipalRef]bool{source: true})
	if !changed || len(got) != 2 || got[0].Name != "mixed" || len(got[0].Roles) != 1 || got[0].Roles[0].Ref != target || got[1].Name != "public" || len(got[1].Roles) != 1 || !got[1].Roles[0].Public {
		t.Fatal(got, changed)
	}
	for i, n := range before {
		if len(old[i].Roles) != n {
			t.Fatal("policy input mutated", old)
		}
	}
	if same, changed := OwnedPolicies(old, map[authorization.PrincipalRef]bool{principals.Principal(102): true}); changed || &same[0] != &old[0] {
		t.Fatal("unrelated source copied policies", same, changed)
	}
}
