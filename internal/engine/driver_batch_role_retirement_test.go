package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"testing"
)

// These guards inspect real owner and catalog roots. SQL eligibility and
// PostgreSQL's physical role-object wait belong to the shared reference cases.
func TestDriverBatchRoleRetirementPublicationGuard(t *testing.T) {
	for _, dependency := range []string{"schema", "policy"} {
		for _, carrier := range []string{"ordinary", "clone", "promotion", "savepoint", "prepared"} {
			t.Run(dependency+"/"+carrier, func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, b, "CREATE ROLE retired LOGIN")
				sessionSQL(t, b, "CREATE TABLE retirement_rows(i INT)")
				sessionSQL(t, b, "CREATE TABLE retirement_policy(i INT)")
				role, ok := e.Roles().Lookup("retired")
				if !ok {
					t.Fatal("missing committed role")
				}
				m := batchUnitStart(t, a, context.Background())
				defer m.end(context.Canceled)
				for _, q := range []string{"INSERT INTO retirement_rows VALUES(8)", "DROP ROLE retired"} {
					if _, err := m.step(statement(t, q)); err != nil {
						t.Fatal(q, err)
					}
				}
				if carrier == "clone" {
					a.Transactions.Catalog = a.Transactions.Catalog.Clone().Clone()
				}
				if carrier == "promotion" || carrier == "savepoint" || carrier == "prepared" {
					if _, err := m.step(statement(t, "BEGIN")); err != nil {
						t.Fatal(err)
					}
				}
				if carrier == "savepoint" {
					for _, q := range []string{"SAVEPOINT kept", "INSERT INTO retirement_rows VALUES(9)", "ROLLBACK TO kept", "RELEASE kept"} {
						if _, err := m.step(statement(t, q)); err != nil {
							t.Fatal(q, err)
						}
					}
				}
				var prepared *session.PreparedTransaction
				if carrier == "prepared" {
					if _, err := m.step(statement(t, "PREPARE TRANSACTION 'retirement_guard'")); err != nil {
						t.Fatal(err)
					}
				}
				if carrier == "promotion" || carrier == "savepoint" || carrier == "prepared" {
					if err := m.end(nil); err != nil {
						t.Fatal(err)
					}
					if carrier == "prepared" {
						prepared = e.prepared.Lookup("retirement_guard")
						if prepared == nil || prepared.Catalog == nil || prepared.Catalog.Roles == nil {
							t.Fatal("detach lost the private role candidate")
						}
					}
				}
				var address ids.ObjectAddress
				var descriptor catalog.Descriptor
				switch dependency {
				case "schema":
					sessionSQL(t, b, "CREATE SCHEMA retirement_peer AUTHORIZATION retired")
					address = schemaRef(t, e, "retirement_peer").Address
					descriptor = e.catalog.State.Get(address)
					if descriptor.(*catalog.Namespace).Owner != role.Ref {
						t.Fatal("peer schema lost its canonical owner")
					}
				case "policy":
					sessionSQL(t, b, "CREATE POLICY retirement_peer ON retirement_policy TO retired USING(i>0)")
					address = catalogRef(t, e, e.CatalogRead(), "retirement_policy").Address
					descriptor = e.catalog.State.Get(address)
					policies := descriptor.(*catalog.Relation).Security.Policies
					if len(policies) != 1 || policies[0].OID == 0 || len(policies[0].Roles) != 1 || policies[0].Roles[0].Ref != role.Ref {
						t.Fatal("peer policy lost its actual identity or selector", policies)
					}
				}
				base, roles, frontier := e.catalog.State, e.roles.Published, e.visibility.CommitSeq()
				var err error
				if carrier == "ordinary" || carrier == "clone" {
					err = m.end(nil)
				} else if carrier == "prepared" {
					// Completion is a new singleton command, never a retained
					// message: the existing prepared block gate must stay intact.
					op, _, acquireErr := a.AcquireCommand(context.Background())
					if acquireErr != nil {
						t.Fatal(acquireErr)
					}
					scope, startErr := e.StartStatement(op)
					if startErr != nil {
						op.Done()
						t.Fatal(startErr)
					}
					_, err = scope.Execute(statement(t, "COMMIT PREPARED 'retirement_guard'"), 0, nil)
					if end := scope.Finish(err); err == nil {
						err = end
					}
					op.Done()
				} else {
					finish := batchUnitStart(t, a, context.Background())
					defer finish.end(context.Canceled)
					q := "COMMIT"
					_, err = finish.step(statement(t, q))
					if end := finish.end(err); err == nil {
						err = end
					}
				}
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("invalid retirement candidate published", err)
				}
				if e.catalog.State != base || e.roles.Published != roles || e.visibility.CommitSeq() != frontier || e.catalog.State.Get(address) != descriptor {
					t.Fatal("rejected retirement changed committed peer roots or publication frontier")
				}
				if current, ok := e.Roles().Lookup("retired"); !ok || current.Ref != role.Ref {
					t.Fatal("rejected retirement orphaned the canonical peer principal")
				}
				if carrier == "prepared" {
					if e.prepared.Lookup("retirement_guard") != prepared || prepared.Claimed {
						t.Fatal("failed final completion consumed or claimed detached authority")
					}
					sessionSQL(t, a, "ROLLBACK PREPARED 'retirement_guard'")
				}
				transactionRows(t, b, "SELECT i FROM retirement_rows")
			})
		}
	}
}

func TestDriverBatchRoleRetirementCandidateRestoration(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE ROLE restored LOGIN")
	sessionSQL(t, b, "CREATE ROLE later LOGIN")
	role, _ := e.Roles().Lookup("restored")
	m := batchUnitStart(t, a, context.Background())
	defer m.end(context.Canceled)
	// This is the catalog-root portion of a saved owner candidate, not a claim
	// that native explicit-block DROP ROLE is eligible after SQL SAVEPOINT.
	before := a.Transactions.Catalog.Clone()
	if _, err := m.step(statement(t, "DROP ROLE restored")); err != nil {
		t.Fatal(err)
	}
	retirement := a.Transactions.Catalog.Clone()
	if _, err := m.step(statement(t, "DROP ROLE later")); err != nil {
		t.Fatal(err)
	}
	if len(retirement.RetiredRoles) != 1 || len(a.Transactions.Catalog.RetiredRoles) != 2 {
		t.Fatal("later retirement mutated the saved candidate's obligation set")
	}
	if _, ok := retirement.RetiredRoles[role.Ref]; !ok {
		t.Fatal("retirement fork lost the exact principal obligation")
	}
	a.Transactions.Catalog = before.Clone()
	sessionSQL(t, b, "CREATE SCHEMA restoration_peer AUTHORIZATION restored")
	if retirement.Roles == nil || retirement.Roles.Root.Read(authorization.PrincipalRef{}).LookupRef("restored") != (authorization.PrincipalRef{}) {
		t.Fatal("retirement fork did not carry the private deletion")
	}
	if err := m.end(nil); err != nil {
		t.Fatal("restored pre-retirement candidate must not retain a deletion obligation", err)
	}
	if current, ok := e.Roles().Lookup("restored"); !ok || current.Ref != role.Ref {
		t.Fatal("candidate restoration deleted its still-dependent principal")
	}
	address := schemaRef(t, e, "restoration_peer").Address
	if e.catalog.State.MustNamespace(address).Owner != role.Ref {
		t.Fatal("candidate restoration changed the unrelated peer root")
	}
}

func TestDriverBatchRoleRetirementMembership(t *testing.T) {
	for _, endpoint := range []string{"endpoint", "member"} {
		t.Run(endpoint, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			for _, name := range []string{"grantor", "endpoint", "member"} {
				sessionSQL(t, a, "CREATE ROLE "+name)
			}
			sessionSQL(t, a, "GRANT endpoint TO grantor WITH ADMIN OPTION")
			grantor, _ := e.Roles().Lookup("grantor")
			role, _ := e.Roles().Lookup("endpoint")
			member, _ := e.Roles().Lookup("member")
			edge := authorization.Membership{Role: role.Ref, Member: member.Ref, Grantor: grantor.Ref, Inherit: true, Set: true}
			if err := grantMembership(t, e, edge); err != nil {
				t.Fatal(err)
			}
			base := e.roles.Published
			before := e.Roles().Memberships()
			rejected := batchUnitStart(t, a, context.Background())
			_, err := rejected.step(statement(t, "DROP ROLE grantor"))
			state(t, err, "2BP01")
			_ = rejected.end(err)
			if e.roles.Published != base {
				t.Fatal("admission grantor rejection changed committed membership authority")
			}
			accepted := batchUnitStart(t, a, context.Background())
			defer accepted.end(context.Canceled)
			if _, err := accepted.step(statement(t, "DROP ROLE grantor,"+endpoint)); err != nil {
				t.Fatal(err)
			}
			if e.roles.Published != base {
				t.Fatal("endpoint cleanup published before true end")
			}
			if err := accepted.end(nil); err != nil {
				t.Fatal("own endpoint cleanup must discharge the grantor obligation", err)
			}
			if _, ok := e.Roles().Lookup("grantor"); ok {
				t.Fatal("completed endpoint cleanup did not retire the principal")
			}
			for _, retained := range fixtureEdges(e.roles.Published) {
				if retained == edge {
					t.Fatal("retired endpoint retained its membership dependency")
				}
			}
			if !reflect.DeepEqual(fixtureEdges(base), before) {
				t.Fatal("publication mutated the immutable entry membership root")
			}
		})
	}
}
