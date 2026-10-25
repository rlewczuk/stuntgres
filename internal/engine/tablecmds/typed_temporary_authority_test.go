package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
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
					roles, sessionRevision := e.RoleStore.Published.Revision, reader.Session.Authorization.Revision
					explicit := mode == "explicit_temporary"
					if explicit {
						enginetest.RoleExecute(t, e, reader, "BEGIN")
						defer enginetest.RoleExecute(t, e, reader, "ROLLBACK")
					}
					original := e.Catalog.Providers[ids.RelationClassID]
					defer func() { e.Catalog.Providers[ids.RelationClassID] = original }()
					called := false
					var winner *catalog.State
					e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
						if called {
							return
						}
						called = true
						e.Catalog.Providers[ids.RelationClassID] = original
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
					_, executeErr := scope.Execute(enginetest.Statement(t, query), 0, nil)
					candidate := scope.Catalog
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
					if current.State() != winner || current.Epoch() != before.Epoch() || changed.SQLType != oldType.SQLType || current.Get(source.OwningRelation) != oldBacking || e.RoleStore.Published.Revision != roles || reader.Session.Authorization.Revision != sessionRevision || reflect.DeepEqual(changed.ACL, oldType.ACL) {
						t.Error("rejected CREATE did not retain descriptor-only ACL winner atomically")
					}
					for _, d := range current.Descriptors() {
						if d.Name() == "late" {
							t.Error("failed CREATE published candidate relation or own-row type", d.Address())
						}
					}
					if candidate != nil {
						for a := range candidate.Storage {
							if e.Storage.Relations[a] != nil {
								t.Error("failed CREATE published candidate storage", a)
							}
						}
					}
					if explicit {
						if reader.Session.Transactions.State != session.TxAborted || reader.Session.Transactions.Catalog != nil {
							t.Error("failed admitted statement did not follow existing abort/overlay cleanup")
						}
						enginetest.RoleExecute(t, e, reader, "ROLLBACK")
					}
					fresh := "CREATE TABLE fresh OF pair"
					denied := "pair"
					if member {
						// OF copies members without a new member-USAGE gate. A fresh
						// ordinary declaration independently proves the ACL winner.
						fresh, denied = "CREATE TABLE fresh(v member)", "member"
					}
					enginetest.RoleError(t, e, reader, fresh, "42501", "permission denied for type "+denied, "")
					enginetest.LockEmpty(t, e)
				})
			}
		})
	}
}

func typedCreateACLFixture(t *testing.T, member bool) (*core.Engine, *session.Operation, func(), *catalog.Type, ids.ObjectAddress) {
	t.Helper()
	e, owner, finishOwner, memberAddress := typedReviewACLFixture(t)
	if member {
		enginetest.RoleExecute(t, e, owner, "DROP TABLE typed")
		enginetest.RoleExecute(t, e, owner, "DROP TYPE pair")
		enginetest.RoleExecute(t, e, owner, "CREATE TYPE pair AS (id member)")
	}
	backing := enginetest.RoleTableRef(t, e, "pair")
	sourceRelation := e.CatalogRead().MustRelation(backing.Address)
	source := e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sourceRelation.RowType)})
	reader, done := enginetest.RoleOperation(t, e, "typed_reader")
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
	enginetest.RoleExecute(t, e, reader, "BEGIN")
	defer enginetest.RoleExecute(t, e, reader, "ROLLBACK")
	enginetest.RoleExecute(t, e, reader, "CREATE TEMP TABLE late OF pair")
	private := reader.Session.Transactions.Catalog
	var relation *catalog.Relation
	for _, d := range private.Descriptors {
		if table, ok := d.(*catalog.Relation); ok && table.Name() == "late" {
			relation = table
		}
	}
	if relation == nil || relation.OfType != source.Address() || private.Storage[relation.Address()].Replacement == nil {
		t.Fatal("successful statement did not install actual private TEMP attachment/store")
	}
	typedReviewTypeACL(t, e, observed, authorization.ACLValue{Explicit: true})
	winner := e.CatalogRead().Get(observed)
	enginetest.RoleExecute(t, e, reader, "COMMIT")
	view := e.CatalogRead()
	if view.Get(relation.Address()) != relation || view.Get(observed) != winner || e.Storage.Relations[relation.Address()] == nil || relation.OfType != source.Address() {
		t.Fatal("outer COMMIT reauthorized successful statement or lost attachment/store/ACL winner")
	}
	enginetest.RoleError(t, e, reader, "CREATE TEMP TABLE fresh OF pair", "42501", "permission denied for type pair", "")
	enginetest.LockEmpty(t, e)
}

// Publish real immutable type ACL descriptors, not a replacement SQL codec or
// role/session revision. Type-only ACL replacement is the established seam.
func typedReviewTypeACL(t *testing.T, e *core.Engine, address ids.ObjectAddress, acl authorization.ACLValue) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	next := *stage.Base.MustType(address)
	next.ACL, next.ObjectRevision = acl, next.ObjectRevision+1
	next.ACLSyntheticDefault = false
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
}

func typedReviewACLFixture(t *testing.T) (*core.Engine, *session.Operation, func(), ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	admin, finish := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, admin, "CREATE USER typed_owner")
	enginetest.RoleExecute(t, e, admin, "CREATE USER typed_reader")
	enginetest.RoleExecute(t, e, admin, "CREATE DOMAIN member AS integer")
	enginetest.RoleExecute(t, e, admin, "CREATE TYPE foreign_pair AS (z integer)")
	ns := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := enginetest.SetObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	finish()
	op, done := enginetest.RoleOperation(t, e, "typed_owner")
	enginetest.RoleExecute(t, e, op, "CREATE TYPE pair AS (id integer)")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE typed OF pair")
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns.Address, Name: "member"})
	return e, op, done, address
}

func domainUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
