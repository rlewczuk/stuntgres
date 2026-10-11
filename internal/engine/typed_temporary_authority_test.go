package engine

import (
	"errors"
	"reflect"
	"testing"
)

// These are actual provider/executor tests: the immutable ACL winner retains
// its codec and backing relation and runs after CREATE's source admission.
func TestTypedPublicationCreateACLConflict(t *testing.T) {
	for _, member := range []bool{false, true} {
		name := "source"
		if member {
			name = "member"
		}
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"persistent", "implicit_temporary", "explicit_temporary"} {
				t.Run(mode, func(t *testing.T) {
					e, reader, done, source, observed := typedCreateACLFixture(t, member)
					defer done()
					before := e.CatalogRead()
					oldType := before.state.descriptors[observed].(*typeDescriptor)
					oldBacking := before.state.descriptors[source.owningRelation]
					roles, authorization := e.roles.revision, reader.session.authorization.revision
					explicit := mode == "explicit_temporary"
					if explicit {
						roleExecute(t, e, reader, "BEGIN")
						defer roleExecute(t, e, reader, "ROLLBACK")
					}
					original := e.providers[RelationClassID]
					defer func() { e.providers[RelationClassID] = original }()
					called := false
					var winner *catalogState
					e.providers[RelationClassID] = alterDropProvider{original, func() {
						if called {
							return
						}
						called = true
						e.providers[RelationClassID] = original
						typedReviewTypeACL(t, e, observed, ACLValue{Explicit: true})
						winner = e.CatalogRead().state
					}}
					query := "CREATE TABLE late OF pair"
					if mode != "persistent" {
						query = "CREATE TEMP TABLE late OF pair"
					}
					scope, err := e.StartStatement(reader)
					if err != nil {
						t.Fatal(err)
					}
					defer scope.Finish(errors.New("unit cleanup"))
					_, executeErr := scope.Execute(statement(t, query), 0, nil)
					candidate := scope.catalog
					finishErr := scope.Finish(executeErr)
					if !called {
						t.Fatal("actual CREATE did not reach provider callback")
					}
					if mode == "persistent" {
						// Existing persistent CREATE maps its exact-descriptor conflict
						// to the established destination conflict diagnostic.
						if executeErr == nil || finishErr == nil {
							t.Fatal("persistent CREATE admitted changed consulted ACL", executeErr, finishErr)
						}
					} else {
						var conflict CatalogConflict
						if executeErr != nil || !errors.As(finishErr, &conflict) {
							t.Error("TEMP Finish admitted changed consulted ACL", executeErr, finishErr)
						}
						if candidate == nil {
							t.Fatal("TEMP executor did not stage real private catalog")
						}
					}
					current := e.CatalogRead()
					changed := current.state.descriptors[observed].(*typeDescriptor)
					if current.state != winner || current.Epoch() != before.Epoch() || changed.typ != oldType.typ || current.state.descriptors[source.owningRelation] != oldBacking || e.roles.revision != roles || reader.session.authorization.revision != authorization || reflect.DeepEqual(changed.acl, oldType.acl) {
						t.Error("rejected CREATE did not retain descriptor-only ACL winner atomically")
					}
					for _, d := range current.state.descriptors {
						if d.Name() == "late" {
							t.Error("failed CREATE published candidate relation or own-row type", d.Address())
						}
					}
					if candidate != nil {
						for a := range candidate.storage {
							if e.storage[a] != nil {
								t.Error("failed CREATE published candidate storage", a)
							}
						}
					}
					if explicit {
						if reader.session.transactions.state != TxAborted || reader.session.transactions.catalog != nil {
							t.Error("failed admitted statement did not follow existing abort/overlay cleanup")
						}
						roleExecute(t, e, reader, "ROLLBACK")
					}
					fresh := "CREATE TABLE fresh OF pair"
					denied := "pair"
					if member {
						// OF copies members without a new member-USAGE gate. A fresh
						// ordinary declaration independently proves the ACL winner.
						fresh, denied = "CREATE TABLE fresh(v member)", "member"
					}
					roleError(t, e, reader, fresh, "42501", "permission denied for type "+denied, "")
					lockEmpty(t, e)
				})
			}
		})
	}
}

func typedCreateACLFixture(t *testing.T, member bool) (*Engine, *Operation, func(), *typeDescriptor, ObjectAddress) {
	t.Helper()
	e, owner, finishOwner, memberAddress := typedReviewACLFixture(t)
	if member {
		roleExecute(t, e, owner, "DROP TABLE typed")
		roleExecute(t, e, owner, "DROP TYPE pair")
		roleExecute(t, e, owner, "CREATE TYPE pair AS (id member)")
	}
	backing := roleTableRef(t, e, "pair")
	sourceRelation := e.CatalogRead().state.descriptors[backing.Address].(*relationDescriptor)
	source := e.CatalogRead().state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(sourceRelation.rowType)}].(*typeDescriptor)
	reader, done := roleOperation(t, e, "typed_reader")
	grant := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: source.owner, Grantee: ACLGrantee{Role: reader.RoleState().CurrentUser}, Privileges: UsagePrivilege}}}
	typedReviewTypeACL(t, e, source.Address(), grant)
	source = e.CatalogRead().state.descriptors[source.Address()].(*typeDescriptor)
	observed := source.Address()
	if member {
		memberType := e.CatalogRead().state.descriptors[memberAddress].(*typeDescriptor)
		grant.Entries[0].Grantor = memberType.owner
		typedReviewTypeACL(t, e, memberAddress, grant)
		observed = memberAddress
	}
	finishOwner()
	return e, reader, done, source, observed
}

func TestTypedPublicationTemporaryAuthorityExpiresAtFinish(t *testing.T) {
	e, reader, done, source, observed := typedCreateACLFixture(t, false)
	defer done()
	roleExecute(t, e, reader, "BEGIN")
	defer roleExecute(t, e, reader, "ROLLBACK")
	roleExecute(t, e, reader, "CREATE TEMP TABLE late OF pair")
	private := reader.session.transactions.catalog
	var relation *relationDescriptor
	for _, d := range private.descriptors {
		if table, ok := d.(*relationDescriptor); ok && table.name == "late" {
			relation = table
		}
	}
	if relation == nil || relation.ofType != source.Address() || private.storage[relation.address].replacement == nil {
		t.Fatal("successful statement did not install actual private TEMP attachment/store")
	}
	typedReviewTypeACL(t, e, observed, ACLValue{Explicit: true})
	winner := e.CatalogRead().state.descriptors[observed]
	roleExecute(t, e, reader, "COMMIT")
	view := e.CatalogRead()
	if view.state.descriptors[relation.address] != relation || view.state.descriptors[observed] != winner || e.storage[relation.address] == nil || relation.ofType != source.Address() {
		t.Fatal("outer COMMIT reauthorized successful statement or lost attachment/store/ACL winner")
	}
	roleError(t, e, reader, "CREATE TEMP TABLE fresh OF pair", "42501", "permission denied for type pair", "")
	lockEmpty(t, e)
}
