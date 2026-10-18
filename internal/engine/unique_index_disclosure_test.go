package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// Native SQL ACL provisioning is not supported. Exercise the actual catalog ACL
// authority as the counterpart of the shared pinned PostgreSQL owner-REVOKE phase.
func TestUniqueIndexBuildOwnerDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := New()
				admin, adminDone := roleOperation(t, e, "postgres")
				defer adminDone()
				roleExecute(t, e, admin, "CREATE ROLE ui_owner LOGIN")
				owner, ownerDone := roleOperation(t, e, "ui_owner")
				defer ownerDone()
				withPublicSchemaUnitOwner(t, e, "ui_owner", func() {
					roleExecute(t, e, owner, "CREATE TABLE ui_t(a INT,b TEXT)")
					roleExecute(t, e, owner, "INSERT INTO ui_t VALUES(1,'protected'),(1,'protected')")
					ref := roleTableRef(t, e, "ui_t")
					actor, ok := e.Roles().Lookup("ui_owner")
					if !ok || actor.Superuser {
						t.Fatal("owner must be nonsuperuser", actor, ok)
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
					if mode == "table" {
						rights |= authorization.SelectPrivilege
					}
					grant(0, rights)
					if mode == "partial" || mode == "all" {
						grant(1, authorization.SelectPrivilege)
					}
					if mode == "all" {
						grant(2, authorization.SelectPrivilege)
					}
					before, store := e.catalog.state, e.storage[ref.Address]
					ast := statement(t, "CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)")
					var result Result
					var err error
					if prepared {
						scope, startErr := e.StartStatement(owner)
						if startErr != nil {
							t.Fatal(startErr)
						}
						description, describeErr := scope.Describe(ast)
						if describeErr != nil {
							scope.Finish(describeErr)
							t.Fatal(describeErr)
						}
						scope.Expected = &description
						result, err = scope.Execute(ast, 0, nil)
						scope.Finish(err)
					} else {
						result, err = e.Execute(owner, ast, 0, nil)
					}
					if result.Count != 0 || result.Rows != nil {
						t.Fatal("failed build returned output", result)
					}
					detail := "Duplicate keys exist."
					if mode == "all" || mode == "table" {
						detail = "Key (a, b)=(1, protected) is duplicated."
					}
					nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui_ab"`, Detail: detail, Schema: "public", Table: "ui_t", Constraint: "ui_ab"})
					if e.catalog.state != before || e.storage[ref.Address] != store || len(e.indexRoots) != 0 {
						t.Fatal("failed build published state")
					}
					want := [][]driver.Value{{int64(1), "protected"}, {int64(1), "protected"}}
					if got := tuples(roleExecute(t, e, admin, "SELECT a,b FROM ui_t")); !reflect.DeepEqual(got, want) {
						t.Fatal(got, want)
					}
					lockEmpty(t, e)
				})
			})
		}
	}
}

func TestUniqueIndexCurrentACLAfterWait(t *testing.T) {
	for _, initiallyVisible := range []bool{false, true} {
		t.Run(map[bool]string{false: "grant_during_wait", true: "revoke_during_wait"}[initiallyVisible], func(t *testing.T) {
			e := New()
			admin, adminDone := roleOperation(t, e, "postgres")
			defer adminDone()
			roleExecute(t, e, admin, "CREATE ROLE ui_owner LOGIN")
			owner, ownerDone := roleOperation(t, e, "ui_owner")
			defer ownerDone()
			withPublicSchemaUnitOwner(t, e, "ui_owner", func() {
				roleExecute(t, e, owner, "CREATE TABLE public.ui_t(k INT)")
				roleExecute(t, e, owner, "CREATE UNIQUE INDEX ui_k ON public.ui_t(k)")
			})
			ref := roleTableRef(t, e, "public.ui_t")
			actor, ok := e.Roles().Lookup("ui_owner")
			if !ok || actor.Superuser {
				t.Fatal(actor, ok)
			}
			grant := func(visible bool) {
				rights := authorization.InsertPrivilege
				if visible {
					rights |= authorization.SelectPrivilege
				}
				acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: rights}}}
				if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
					t.Fatal(err)
				}
			}
			grant(initiallyVisible)
			owner.Done()
			holder := sessionStart(t, e, nil)
			transactionBegin(t, holder)
			sessionSQL(t, holder, "INSERT INTO public.ui_t VALUES(7)")
			done, _ := uniqueAsyncUnit(t, owner.session, "INSERT INTO public.ui_t VALUES(7)")
			lockPending(t, e, 1)
			grant(!initiallyVisible)
			sessionSQL(t, holder, "COMMIT")
			select {
			case outcome := <-done:
				if outcome.result.Count != 0 || outcome.result.Rows != nil {
					t.Fatal(outcome.result)
				}
				detail := ""
				if !initiallyVisible {
					detail = "Key (k)=(7) already exists."
				}
				nullCountUnitDiagnostic(t, outcome.err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui_k"`, Detail: detail, Schema: "public", Table: "ui_t", Constraint: "ui_k"})
			case <-time.After(5 * time.Second):
				t.Fatal("unique waiter did not finish after holder commit")
			}
			uniqueClaimsEmpty(t, e)
		})
	}
}
