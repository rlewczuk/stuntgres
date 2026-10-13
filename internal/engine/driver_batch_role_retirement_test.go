package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"testing"
)

// These guards inspect real owner/catalog/registry roots. SQL eligibility and
// PostgreSQL's physical role-object wait belong to the shared reference cases.
func TestDriverBatchRoleRetirementPublicationGuard(t *testing.T) {
	for _, dependency := range []string{"schema", "policy", "registered"} {
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
					a.transactions.catalog = cloneCatalogChanges(cloneCatalogChanges(a.transactions.catalog))
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
				var prepared *preparedTransaction
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
						prepared = e.prepared["retirement_guard"]
						if prepared == nil || prepared.catalog == nil || prepared.catalog.roles == nil {
							t.Fatal("detach lost the private role candidate")
						}
					}
				}
				var address ObjectAddress
				var descriptor Descriptor
				switch dependency {
				case "schema":
					sessionSQL(t, b, "CREATE SCHEMA retirement_peer AUTHORIZATION retired")
					address = schemaRef(t, e, "retirement_peer").Address
					descriptor = e.state.descriptors[address]
					if descriptor.(*namespaceDescriptor).owner != role.Ref {
						t.Fatal("peer schema lost its canonical owner")
					}
				case "policy":
					sessionSQL(t, b, "CREATE POLICY retirement_peer ON retirement_policy TO retired USING(i>0)")
					address = catalogRef(t, e, e.CatalogRead(), "retirement_policy").Address
					descriptor = e.state.descriptors[address]
					policies := descriptor.(*relationDescriptor).policies
					if len(policies) != 1 || policies[0].OID == 0 || len(policies[0].roles) != 1 || policies[0].roles[0].ref != role.Ref {
						t.Fatal("peer policy lost its actual identity or selector", policies)
					}
				case "registered":
					if err := e.SetRoleReferences(context.Background(), "retirement guard", []authorization.PrincipalRef{role.Ref}); err != nil {
						t.Fatal(err)
					}
				}
				base, roles, frontier := e.state, e.roles, e.commitSeq
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
				if err == nil || dependency != "registered" && !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal("invalid retirement candidate published", err)
				}
				if e.state != base || e.roles != roles || e.commitSeq != frontier || address != (ObjectAddress{}) && e.state.descriptors[address] != descriptor {
					t.Fatal("rejected retirement changed committed peer roots or publication frontier")
				}
				if current, ok := e.Roles().Lookup("retired"); !ok || current.Ref != role.Ref {
					t.Fatal("rejected retirement orphaned the canonical peer principal")
				}
				if dependency == "registered" && !reflect.DeepEqual(e.roleReferences["retirement guard"], []authorization.PrincipalRef{role.Ref}) {
					t.Fatal("rejected retirement changed registered principal dependencies")
				}
				if carrier == "prepared" {
					if e.prepared["retirement_guard"] != prepared || prepared.claimed {
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
	before := cloneCatalogChanges(a.transactions.catalog)
	if _, err := m.step(statement(t, "DROP ROLE restored")); err != nil {
		t.Fatal(err)
	}
	retirement := cloneCatalogChanges(a.transactions.catalog)
	if _, err := m.step(statement(t, "DROP ROLE later")); err != nil {
		t.Fatal(err)
	}
	if len(retirement.retiredRoles) != 1 || len(a.transactions.catalog.retiredRoles) != 2 {
		t.Fatal("later retirement mutated the saved candidate's obligation set")
	}
	if _, ok := retirement.retiredRoles[role.Ref]; !ok {
		t.Fatal("retirement fork lost the exact principal obligation")
	}
	a.transactions.catalog = cloneCatalogChanges(before)
	sessionSQL(t, b, "CREATE SCHEMA restoration_peer AUTHORIZATION restored")
	if retirement.roles == nil || retirement.roles.root.Read(authorization.PrincipalRef{}).LookupRef("restored") != (authorization.PrincipalRef{}) {
		t.Fatal("retirement fork did not carry the private deletion")
	}
	if err := m.end(nil); err != nil {
		t.Fatal("restored pre-retirement candidate must not retain a deletion obligation", err)
	}
	if current, ok := e.Roles().Lookup("restored"); !ok || current.Ref != role.Ref {
		t.Fatal("candidate restoration deleted its still-dependent principal")
	}
	address := schemaRef(t, e, "restoration_peer").Address
	if e.state.descriptors[address].(*namespaceDescriptor).owner != role.Ref {
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
			base := e.roles
			before := e.Roles().Memberships()
			rejected := batchUnitStart(t, a, context.Background())
			_, err := rejected.step(statement(t, "DROP ROLE grantor"))
			state(t, err, "2BP01")
			_ = rejected.end(err)
			if e.roles != base {
				t.Fatal("admission grantor rejection changed committed membership authority")
			}
			accepted := batchUnitStart(t, a, context.Background())
			defer accepted.end(context.Canceled)
			if _, err := accepted.step(statement(t, "DROP ROLE grantor,"+endpoint)); err != nil {
				t.Fatal(err)
			}
			if e.roles != base {
				t.Fatal("endpoint cleanup published before true end")
			}
			if err := accepted.end(nil); err != nil {
				t.Fatal("own endpoint cleanup must discharge the grantor obligation", err)
			}
			if _, ok := e.Roles().Lookup("grantor"); ok {
				t.Fatal("completed endpoint cleanup did not retire the principal")
			}
			for _, retained := range fixtureEdges(e.roles) {
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
