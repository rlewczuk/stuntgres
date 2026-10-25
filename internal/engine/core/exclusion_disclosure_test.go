package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExclusionDisclosure(t *testing.T) {
	for _, expression := range []bool{false, true} {
		for _, disclosure := range []string{"none", "partial", "columns", "table"} {
			t.Run(disclosure+map[bool]string{false: "/simple", true: "/expression"}[expression], func(t *testing.T) {
				e := engine.New()
				admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
				t.Cleanup(adminDone)
				enginetest.RoleExecute(t, e, admin, "CREATE ROLE ex_owner LOGIN")
				owner, ownerDone := enginetest.RoleOperation(t, e, "ex_owner")
				t.Cleanup(ownerDone)
				key := "a"
				if expression {
					key = "lower(a)"
				}
				withPublicSchemaUnitOwner(t, e, "ex_owner", func() {
					enginetest.RoleExecute(t, e, owner, "CREATE TABLE public.t(a TEXT,b INT,CONSTRAINT ex EXCLUDE("+key+" WITH =,b WITH =))")
				})
				enginetest.RoleExecute(t, e, owner, "INSERT INTO public.t VALUES('x',2)")
				ref := enginetest.RoleTableRef(t, e, "public.t")
				actor, ok := e.Roles().Lookup("ex_owner")
				if !ok || actor.Superuser {
					t.Fatal("disclosure fixture requires an actual nonsuperuser")
				}
				grant := func(attribute int32, rights authorization.Privileges) {
					target := ref
					target.Address.SubID = attribute
					acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: rights}}}
					if err := enginetest.SetObjectACL(e, context.Background(), target, acl); err != nil {
						t.Fatal(err)
					}
				}
				rights := authorization.InsertPrivilege
				if disclosure == "table" {
					rights |= authorization.SelectPrivilege
				}
				grant(0, rights)
				if disclosure == "partial" || disclosure == "columns" {
					grant(1, authorization.SelectPrivilege)
				}
				if disclosure == "columns" {
					grant(2, authorization.SelectPrivilege)
				}
				auth, err := engine.OperationAuthorization(owner)
				if err != nil {
					t.Fatal(err)
				}
				parent := e.Catalog.State.MustRelation(ref.Address)
				idx, root := btreeRoot(t, e, "public.ex")
				previous := root.Entries[0]
				current := previous
				current.Row, current.Version = ids.RowID(e.Storage.RowFrontier()+1), ids.VersionID(e.Storage.VersionFrontier()+1)
				visible := disclosure == "table" || disclosure == "columns" && !expression
				for _, building := range []bool{false, true} {
					detail := "Key conflicts with existing key."
					message := `conflicting key value violates exclusion constraint "ex"`
					if building {
						detail, message = "Key conflicts exist.", `could not create exclusion constraint "ex"`
					}
					if visible {
						middle := " conflicts with existing key "
						if building {
							middle = " conflicts with key "
						}
						label := "(" + strings.Join([]string{key, "b"}, ", ") + ")"
						detail = "Key " + label + "=(x, 2)" + middle + label + "=(x, 2)."
					}
					err := index.ValidateExclusionPair(context.Background(), parent, idx, previous, current, "public", sqltype.OutputEnvironment{}, &auth.View, engine.DisclosureEnv(&auth), building)
					nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23P01", Message: message, Detail: detail, Schema: "public", Table: "t", Constraint: "ex"})
				}
			})
		}
	}
}
