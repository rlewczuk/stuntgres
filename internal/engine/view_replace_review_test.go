package engine

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"testing"
)

func TestViewReplaceCreationNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, qualified := range []bool{false, true} {
			t.Run(fmt.Sprintf("qualified=%v/prepared=%v", qualified, prepared), func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{"CREATE USER creator", "CREATE USER other_owner", "CREATE SCHEMA first_schema AUTHORIZATION creator", "CREATE SCHEMA target_schema", "CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(1)", "CREATE VIEW public.v AS SELECT id FROM public.t", "ALTER VIEW public.v OWNER TO other_owner", "CREATE VIEW target_schema.v AS SELECT id FROM public.t", "ALTER VIEW target_schema.v OWNER TO creator", "CREATE VIEW public.w AS SELECT id FROM target_schema.v"} {
					roleExecute(t, e, op, q)
				}
				creator, _ := e.Roles().Lookup("creator")
				otherOwner, _ := e.Roles().Lookup("other_owner")
				admin := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				source := roleTableRef(t, e, "public.t")
				if err := e.SetObjectACL(context.Background(), source, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin, Grantee: authorization.ACLGrantee{Role: creator.Ref}, Privileges: authorization.SelectPrivilege}, {Grantor: admin, Grantee: authorization.ACLGrantee{Role: otherOwner.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
					t.Fatal(err)
				}
				targetNS := e.catalog.Domain.Ref(e.catalog.state.Epoch(), e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "target_schema"})[0])
				// The actor has CREATE and owns the target view, but has no USAGE
				// on the target namespace. Its independently accessible body source
				// must not impose target-schema lookup requirements.
				if err := e.SetObjectACL(context.Background(), targetNS, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin, Grantee: authorization.ACLGrantee{Role: creator.Ref}, Privileges: authorization.CreatePrivilege}}}); err != nil {
					t.Fatal(err)
				}
				later := roleTableRef(t, e, "public.v")
				oldLater := e.CatalogRead().Get(later.Address)
				target := roleTableRef(t, e, "target_schema.v")
				oldTarget := e.CatalogRead().MustRelation(target.Address)
				roleExecute(t, e, op, "SET SESSION AUTHORIZATION creator")
				roleExecute(t, e, op, "SET search_path TO first_schema,public")
				name := "v"
				if qualified {
					name = "target_schema.v"
				}
				stmt := statement(t, "CREATE OR REPLACE VIEW "+name+" AS SELECT id+10 AS id FROM public.t")
				if prepared {
					before := e.CatalogRead().State()
					if _, err := e.Describe(op, stmt); err != nil {
						t.Fatal(err)
					}
					if e.CatalogRead().State() != before {
						t.Fatal("Prepare published")
					}
				}
				if _, err := e.Execute(op, stmt, 0, nil); err != nil {
					t.Fatal(err)
				}
				roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
				if e.CatalogRead().Get(later.Address) != oldLater {
					t.Fatal("later owner relation changed")
				}
				if qualified {
					current := e.CatalogRead().MustRelation(target.Address)
					if current.Owner != oldTarget.Owner || stored.ViewOf(current).RuleOID() != stored.ViewOf(oldTarget).RuleOID() {
						t.Fatal("qualified replacement lost identity or owner")
					}
					if got := tuples(run(t, e, "SELECT id FROM public.w")); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
						t.Fatal(got)
					}
				} else {
					created := roleTableRef(t, e, "first_schema.v")
					if created.Address == later.Address || created.Address == target.Address {
						t.Fatal("creation targeted a later namespace")
					}
					if got := tuples(run(t, e, "SELECT id FROM first_schema.v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
						t.Fatal(got)
					}
				}
				if got := tuples(run(t, e, "SELECT id FROM public.v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
					t.Fatal(got)
				}
			})
		}
	}
}
