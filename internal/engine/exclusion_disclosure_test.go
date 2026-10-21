package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExclusionDisclosure(t *testing.T) {
	for _, expression := range []bool{false, true} {
		for _, disclosure := range []string{"none", "partial", "columns", "table"} {
			t.Run(disclosure+map[bool]string{false: "/simple", true: "/expression"}[expression], func(t *testing.T) {
				e := New()
				admin, adminDone := roleOperation(t, e, "postgres")
				t.Cleanup(adminDone)
				roleExecute(t, e, admin, "CREATE ROLE ex_owner LOGIN")
				owner, ownerDone := roleOperation(t, e, "ex_owner")
				t.Cleanup(ownerDone)
				key := "a"
				if expression {
					key = "lower(a)"
				}
				withPublicSchemaUnitOwner(t, e, "ex_owner", func() {
					roleExecute(t, e, owner, "CREATE TABLE public.t(a TEXT,b INT,CONSTRAINT ex EXCLUDE("+key+" WITH =,b WITH =))")
				})
				roleExecute(t, e, owner, "INSERT INTO public.t VALUES('x',2)")
				ref := roleTableRef(t, e, "public.t")
				actor, ok := e.Roles().Lookup("ex_owner")
				if !ok || actor.Superuser {
					t.Fatal("disclosure fixture requires an actual nonsuperuser")
				}
				grant := func(attribute int32, rights authorization.Privileges) {
					target := ref
					target.Address.SubID = attribute
					acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: rights}}}
					if err := e.SetObjectACL(context.Background(), target, acl); err != nil {
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
				auth, err := operationAuthorization(owner)
				if err != nil {
					t.Fatal(err)
				}
				parent := e.catalog.state.MustRelation(ref.Address)
				index, root := btreeRoot(t, e, "public.ex")
				previous := root.Entries[0]
				current := previous
				current.Row, current.Version = ids.RowID(e.storage.RowFrontier()+1), ids.VersionID(e.storage.VersionFrontier()+1)
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
					err := ValidateExclusionPair(context.Background(), parent, index, previous, current, "public", sqltype.OutputEnvironment{}, &auth.View, disclosureEnv(&auth), building)
					nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23P01", Message: message, Detail: detail, Schema: "public", Table: "t", Constraint: "ex"})
				}
			})
		}
	}
}
