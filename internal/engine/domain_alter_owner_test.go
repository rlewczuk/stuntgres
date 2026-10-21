package engine

import (
	"context"
	"maps"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
)

func domainAlterReplayOwnership(t *testing.T) {
	prefix := "CREATE SCHEMA owned AUTHORIZATION postgres; "
	directives := "SET SESSION AUTHORIZATION 'captured_owner'; SET SESSION AUTHORIZATION 'postgres'; "
	create := "CREATE DOMAIN owned.d AS integer CONSTRAINT upper_bound CHECK(VALUE<10); "
	owner := "ALTER DOMAIN owned.d OWNER TO captured_owner; "
	for _, tc := range []struct {
		name, sql string
		valid     bool
	}{
		{"exact", prefix + directives + create + owner + "CREATE TABLE owned.t(v owned.d); INSERT INTO owned.t VALUES(-1); ALTER DOMAIN owned.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID;", true},
		{"missing_directives", prefix + create + owner, false},
		{"missing_bootstrap_transition", prefix + "SET SESSION AUTHORIZATION 'captured_owner'; " + create + owner, false},
		{"wrong_target_role", prefix + directives + create + "ALTER DOMAIN owned.d OWNER TO other_owner;", false},
		{"identity_target", prefix + directives + create + "ALTER DOMAIN owned.d OWNER TO CURRENT_USER;", false},
		{"unqualified", prefix + directives + create + "ALTER DOMAIN d OWNER TO captured_owner;", false},
		{"wrong_namespace", prefix + directives + create + "ALTER DOMAIN public.d OWNER TO captured_owner;", false},
		{"wrong_kind", prefix + directives + create + "ALTER TYPE owned.d OWNER TO captured_owner;", false},
		{"existing_other_domain", prefix + "CREATE DOMAIN owned.other AS integer; " + directives + create + "ALTER DOMAIN owned.other OWNER TO captured_owner;", false},
		{"intervening_statement", prefix + directives + create + "CREATE TABLE owned.gap(v integer); " + owner, false},
		{"intervening_directive", prefix + directives + create + "SET SESSION AUTHORIZATION 'postgres'; " + owner, false},
		{"reversed_pair", prefix + create + directives + owner, false},
		{"consumed_pair", prefix + directives + create + owner + owner, false},
		{"no_constructor", prefix + directives + owner, false},
		{"enum_not_domain", prefix + directives + "CREATE TYPE owned.d AS ENUM('a'); " + owner, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE ROLE captured_owner", "CREATE ROLE other_owner", "CREATE TABLE public.sentinel(v integer)", "INSERT INTO public.sentinel VALUES(42)"} {
				run(t, e, q)
			}
			before, stores, roots := e.catalog.state, maps.Clone(e.storage.Relations), maps.Clone(e.storage.IndexRoots)
			err := e.Restore(context.Background(), strings.NewReader(tc.sql))
			if !tc.valid {
				if err == nil || e.catalog.state != before || !maps.Equal(e.storage.Relations, stores) || !maps.Equal(e.storage.IndexRoots, roots) {
					t.Fatal("ineligible owner replay changed destination", err)
				}
				message := typedef.RestoreDomainOwnerBoundary
				if tc.name == "wrong_kind" {
					message = table.RestoreInheritedOwnerBoundary
				}
				domainUnitDiagnostic(t, err, "0A000", message)
				return
			}
			if err != nil {
				t.Fatal("exact fresh-domain replay", err)
			}
			view := e.CatalogRead()
			namespace := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "owned"})[0]
			ns := view.MustNamespace(namespace)
			domain := view.MustType(view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: "d"})[0])
			role, ok := e.Roles().Lookup("captured_owner")
			bootstrap, rootOK := e.Roles().Lookup("postgres")
			if !ok || !rootOK || domain.Owner != role.Ref || ns.Owner != bootstrap.Ref || ns.ACL.Explicit || !typedef.DefaultDomainACL(domain) {
				t.Fatal("replay invented namespace ACL or lost real domain ownership/default ACL")
			}
			if len(domain.DomainChecks) != 2 || !domain.DomainChecks[0].NotValid || domain.DomainChecks[1].NotValid {
				t.Fatal("replay lost selected validity state")
			}
			s := sessionStart(t, e, nil)
			transactionRows(t, s, "SELECT v::integer AS v FROM owned.t", -1)
		})
	}
}
