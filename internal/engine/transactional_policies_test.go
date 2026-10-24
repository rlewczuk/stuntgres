package engine

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
)

// These are representation assertions unavailable from the public catalog:
// duplicated ordered role refs, absent CHECK versus explicit fallback, and
// immutable owner-journal recipe retention. SQL behavior lives in reference/.
func TestTransactionalPoliciesPayload(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE ROLE payload_a", "CREATE ROLE payload_b", "CREATE TABLE payload_t(id INT)"} {
		sessionSQL(t, s, q)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "payload_t")
	base := e.catalog.state.MustRelation(ref.Address)
	a, ok := e.Roles().Lookup("payload_a")
	if !ok {
		t.Fatal("role a absent")
	}
	b, ok := e.Roles().Lookup("payload_b")
	if !ok {
		t.Fatal("role b absent")
	}
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "CREATE POLICY original ON payload_t AS RESTRICTIVE FOR UPDATE TO payload_b,payload_a,payload_b USING(id>0)")
	private := func() *catalog.Relation { return s.Transactions.Catalog.Descriptors[ref.Address].(*catalog.Relation) }
	first := private()
	p := first.Security.Policies[0]
	wantRoles := []catalog.PolicyRole{{Ref: b.Ref}, {Ref: a.Ref}, {Ref: b.Ref}}
	if p.Command != catalog.PolicyUpdate || p.Permissive || !reflect.DeepEqual(p.Roles, wantRoles) || p.Check != nil || p.Using == nil {
		t.Fatal("actual private payload", p)
	}
	presentation := stored.PolicyOf(p.Using).Presentation()
	sources := slices.Clone(presentation.Sources())
	recipe, err := stored.PolicySQL(context.Background(), e.CatalogRead(), stored.PolicyOf(p.Using))
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, s, "SAVEPOINT payload_mark")
	sessionSQL(t, s, "ALTER POLICY original ON payload_t WITH CHECK(id>0)")
	explicit := private()
	if explicit == first || explicit.Security.Policies[0].Check == nil || explicit.Security.Policies[0].Using != p.Using || !reflect.DeepEqual(explicit.Security.Policies[0].Roles, wantRoles) {
		t.Fatal("CHECK replacement lost omitted payload")
	}
	sessionSQL(t, s, "ALTER POLICY original ON payload_t TO PUBLIC")
	public := private().Security.Policies[0]
	if !reflect.DeepEqual(public.Roles, []catalog.PolicyRole{{Public: true}}) || public.Command != p.Command || public.Permissive != p.Permissive || public.Using != p.Using || public.Check != explicit.Security.Policies[0].Check {
		t.Fatal("PUBLIC normalization/omitted payload", public)
	}
	sessionSQL(t, s, "ALTER POLICY original ON payload_t RENAME TO renamed")
	if private().Security.Policies[0].OID != p.OID || private().Security.Policies[0].Name != "renamed" {
		t.Fatal("rename changed private identity")
	}
	afterRecipe, err := stored.PolicySQL(context.Background(), e.CatalogRead(), stored.PolicyOf(p.Using))
	if err != nil || afterRecipe != recipe || !reflect.DeepEqual(presentation.Sources(), sources) || first.Security.Policies[0].Name != "original" || first.Security.Policies[0].Check != nil || !reflect.DeepEqual(first.Security.Policies[0].Roles, wantRoles) || len(base.Security.Policies) != 0 {
		t.Fatal("immutable predecessor recipe changed", recipe, afterRecipe, err)
	}
	sessionSQL(t, s, "ROLLBACK TO payload_mark")
	restored := private()
	if !reflect.DeepEqual(restored.Security.Policies, first.Security.Policies) {
		t.Fatal("mark failed to restore exact absent-clause/role representation")
	}
	sessionSQL(t, s, "ALTER POLICY original ON payload_t TO payload_a")
	candidate := private()
	sessionSQL(t, s, "RELEASE payload_mark")
	sessionSQL(t, s, "COMMIT")
	published := e.catalog.state.MustRelation(ref.Address)
	if published != candidate || len(base.Security.Policies) != 0 || !reflect.DeepEqual(published.Security.Policies[0].Roles, []catalog.PolicyRole{{Ref: a.Ref}}) || published.Security.Policies[0].Check != nil || published.Security.Policies[0].Using != p.Using {
		t.Fatal("publication changed actual candidate representation")
	}
}
