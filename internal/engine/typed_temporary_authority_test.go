package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
					oldType := before.MustType(observed)
					oldBacking := before.Get(source.OwningRelation)
					roles, sessionRevision := e.roles.published.Revision, reader.session.authorization.revision
					explicit := mode == "explicit_temporary"
					if explicit {
						roleExecute(t, e, reader, "BEGIN")
						defer roleExecute(t, e, reader, "ROLLBACK")
					}
					original := e.catalog.Providers[ids.RelationClassID]
					defer func() { e.catalog.Providers[ids.RelationClassID] = original }()
					called := false
					var winner *catalog.State
					e.catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
						if called {
							return
						}
						called = true
						e.catalog.Providers[ids.RelationClassID] = original
						typedReviewTypeACL(t, e, observed, authorization.ACLValue{Explicit: true})
						winner = e.CatalogRead().State()
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
						var conflict catalog.Conflict
						if executeErr != nil || !errors.As(finishErr, &conflict) {
							t.Error("TEMP Finish admitted changed consulted ACL", executeErr, finishErr)
						}
						if candidate == nil {
							t.Fatal("TEMP executor did not stage real private catalog")
						}
					}
					current := e.CatalogRead()
					changed := current.MustType(observed)
					if current.State() != winner || current.Epoch() != before.Epoch() || changed.SQLType != oldType.SQLType || current.Get(source.OwningRelation) != oldBacking || e.roles.published.Revision != roles || reader.session.authorization.revision != sessionRevision || reflect.DeepEqual(changed.ACL, oldType.ACL) {
						t.Error("rejected CREATE did not retain descriptor-only ACL winner atomically")
					}
					for _, d := range current.Descriptors() {
						if d.Name() == "late" {
							t.Error("failed CREATE published candidate relation or own-row type", d.Address())
						}
					}
					if candidate != nil {
						for a := range candidate.storage {
							if e.storage.Relations[a] != nil {
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

func typedCreateACLFixture(t *testing.T, member bool) (*Engine, *Operation, func(), *catalog.Type, ids.ObjectAddress) {
	t.Helper()
	e, owner, finishOwner, memberAddress := typedReviewACLFixture(t)
	if member {
		roleExecute(t, e, owner, "DROP TABLE typed")
		roleExecute(t, e, owner, "DROP TYPE pair")
		roleExecute(t, e, owner, "CREATE TYPE pair AS (id member)")
	}
	backing := roleTableRef(t, e, "pair")
	sourceRelation := e.CatalogRead().MustRelation(backing.Address)
	source := e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sourceRelation.RowType)})
	reader, done := roleOperation(t, e, "typed_reader")
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: source.Owner, Grantee: authorization.ACLGrantee{Role: reader.RoleState().CurrentUser}, Privileges: authorization.UsagePrivilege}}}
	typedReviewTypeACL(t, e, source.Address(), grant)
	source = e.CatalogRead().MustType(source.Address())
	observed := source.Address()
	if member {
		memberType := e.CatalogRead().MustType(memberAddress)
		grant.Entries[0].Grantor = memberType.Owner
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
	var relation *catalog.Relation
	for _, d := range private.descriptors {
		if table, ok := d.(*catalog.Relation); ok && table.Name() == "late" {
			relation = table
		}
	}
	if relation == nil || relation.OfType != source.Address() || private.storage[relation.Address()].replacement == nil {
		t.Fatal("successful statement did not install actual private TEMP attachment/store")
	}
	typedReviewTypeACL(t, e, observed, authorization.ACLValue{Explicit: true})
	winner := e.CatalogRead().Get(observed)
	roleExecute(t, e, reader, "COMMIT")
	view := e.CatalogRead()
	if view.Get(relation.Address()) != relation || view.Get(observed) != winner || e.storage.Relations[relation.Address()] == nil || relation.OfType != source.Address() {
		t.Fatal("outer COMMIT reauthorized successful statement or lost attachment/store/ACL winner")
	}
	roleError(t, e, reader, "CREATE TEMP TABLE fresh OF pair", "42501", "permission denied for type pair", "")
	lockEmpty(t, e)
}
