package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func namespaceTemporaryStorageFixture(t *testing.T) (*core.Engine, *session.Session, *session.Session, *catalog.Relation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	owner, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE public.storage_parent(v INT)",
		"INSERT INTO public.storage_parent VALUES(7)",
		"CREATE TEMP TABLE storage_foreign(v INT)",
		"INSERT INTO storage_foreign VALUES(99)",
		"CREATE TEMP TABLE storage_child() INHERITS(public.storage_parent)",
		"INSERT INTO storage_child VALUES(88)",
		"CREATE TEMP VIEW storage_view AS SELECT v FROM storage_foreign",
		"CREATE TEMP VIEW storage_chain AS SELECT v FROM storage_view",
	} {
		enginetest.SessionSQL(t, owner, q)
	}
	view := e.CatalogRead()
	namespace := owner.TemporaryNamespace
	enginetest.AssertTemporaryNamespacePair(t, e, owner.SessionToken, namespace)
	lookup := func(namespace ids.ObjectAddress, name string) *catalog.Relation {
		address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
		return view.MustRelation(address)
	}
	return e, owner, peer, lookup(namespace, "storage_foreign"), lookup(namespace, "storage_chain"), lookup(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "storage_parent")
}
