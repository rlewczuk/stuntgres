package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func assertTemporaryNamespacePair(t *testing.T, e *Engine, token ids.SessionToken, table ids.ObjectAddress) ids.ObjectAddress {
	t.Helper()
	ns, ok := e.catalog.state.Namespace(table)
	if !ok || ns.Temporary != token || ns.TemporaryToast || ns.Owner.ID() != 10 || ns.ACL.Explicit || len(ns.ACL.Entries) != 0 {
		t.Fatal("real table TEMP authority", ns)
	}
	suffix, ok := strings.CutPrefix(ns.Name(), "pg_temp_")
	if !ok {
		t.Fatal(ns.Name())
	}
	var toast ids.ObjectAddress
	count := 0
	for a, d := range e.catalog.state.Descriptors() {
		other, ok := d.(*catalog.Namespace)
		if !ok || other.Temporary != token {
			continue
		}
		count++
		if other.TemporaryToast {
			if other.Name() != "pg_toast_temp_"+suffix || other.Owner != ns.Owner || other.ACL.Explicit || len(other.ACL.Entries) != 0 || other.Address() == table {
				t.Fatal("real toast TEMP authority", other)
			}
			toast = a
		}
		if e.catalog.state.NameAddress(catalog.DescriptorNameKey(other)) != a {
			t.Fatal("TEMP name identity", other)
		}
	}
	if count != 2 || toast == (ids.ObjectAddress{}) {
		t.Fatal("TEMP pair cardinality", count, toast)
	}
	return toast
}

func TestSequenceTemporaryNamespacePairAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE ROLE namespace_actor")
	sessionSQL(t, s, "ALTER DEFAULT PRIVILEGES FOR ROLE namespace_actor GRANT USAGE,CREATE ON SCHEMAS TO PUBLIC")
	sessionSQL(t, s, "SET ROLE namespace_actor")
	sessionSQL(t, s, "CREATE TEMP TABLE namespace_table(i INT)")
	table := s.TemporaryNamespace
	toast := assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	object := e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table, Name: "namespace_table"}))
	actor := e.Roles().LookupRef("namespace_actor")
	if object.Owner != actor || object.Owner.ID() == 10 {
		t.Fatal("namespace owner replaced object actor", object.Owner)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	for _, a := range []ids.ObjectAddress{table, toast} {
		ns := auth.Catalog.MustNamespace(a)
		rights, err := auth.Effective(e.catalog.Domain.Ref(auth.Catalog.Epoch(), a), false)
		want := authorization.Privileges(0)
		if a == table {
			want = authorization.UsagePrivilege | authorization.CreatePrivilege
		}
		if err != nil || rights != want || ownTemporaryNamespace(auth, ns) != (a == table) {
			op.Done()
			t.Fatal("table versus toast authority", a, rights, err)
		}
	}
	auth.stagedTemporary = toast
	if temporaryNamespace(auth) != table {
		op.Done()
		t.Fatal("toast impersonated active table namespace")
	}
	s.TemporaryNamespace = ids.ObjectAddress{}
	if temporaryNamespace(auth) != (ids.ObjectAddress{}) {
		op.Done()
		t.Fatal("abandoned pointer regained table authority")
	}
	s.TemporaryNamespace = table
	op.Done()
	retained := namespaceUnitCapture(t, e, e.CatalogRead())
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.TemporaryNamespace != (ids.ObjectAddress{}) || e.catalog.state.Get(object.Address()) != nil {
		t.Fatal("physical cleanup left active object authority")
	}
	assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	for _, id := range []ids.ObjectAddress{table, toast} {
		namespaceUnitRow(t, retained, uint32(id.ObjectID))
		namespaceUnitRow(t, namespaceUnitCapture(t, e, e.CatalogRead()), uint32(id.ObjectID))
	}
}

func TestSequenceTemporaryNamespacePairPrivate(t *testing.T) {
	e := New()
	creator := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	for _, commit := range []bool{false, true} {
		sessionSQL(t, creator, "BEGIN")
		sessionSQL(t, creator, "CREATE TEMP TABLE namespace_private(i INT)")
		op, err := creator.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		auth, err := operationAuthorization(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		private := namespaceUnitCapture(t, e, auth.Catalog)
		committed := namespaceUnitCapture(t, e, e.CatalogRead())
		count := 0
		for _, d := range auth.Catalog.Descriptors() {
			if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == creator.SessionToken {
				count++
				namespaceUnitRow(t, private, uint32(ns.Address().ObjectID))
				if e.catalog.state.Get(ns.Address()) != nil {
					op.Done()
					t.Fatal("uncommitted pair leaked")
				}
			}
		}
		if count != 2 || committed.Count() != 4 {
			op.Done()
			t.Fatal("private paired inventory", count, committed.Count())
		}
		op.Done()
		if commit {
			sessionSQL(t, creator, "COMMIT")
			assertTemporaryNamespacePair(t, e, creator.SessionToken, creator.TemporaryNamespace)
		} else {
			sessionSQL(t, creator, "ROLLBACK")
			if namespaceUnitCapture(t, e, e.CatalogRead()).Count() != 4 {
				t.Fatal("rollback published TEMP pair")
			}
		}
	}
	if peer.TemporaryNamespace != (ids.ObjectAddress{}) {
		t.Fatal("peer acquired creator table namespace")
	}
}

func TestTemporaryNamespaceCollationRetirement(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP TABLE namespace_collation_table(i INT)")
	table := s.TemporaryNamespace
	toast := assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	sessionSQL(t, s, "CREATE COLLATION pg_temp.namespace_collation FROM \"C\"")
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: table, Name: "namespace_collation"})
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.endTemporaryCollations(s); err != nil {
		t.Fatal(err)
	}
	assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	if e.catalog.state.Get(address) != nil {
		t.Fatal("alternate cleanup retained collation child")
	}
	for _, d := range e.catalog.state.Descriptors() {
		if d.Namespace() == table || d.Namespace() == toast {
			t.Fatal("alternate cleanup retained TEMP dependent", d)
		}
	}
}

func TestSequenceTemporaryNamespacePairAllocationFailure(t *testing.T) {
	for _, identity := range []bool{false, true} {
		e := New()
		s := sessionStart(t, e, nil)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		auth, err := operationAuthorization(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		base := e.catalog.state
		e.mu.Lock()
		if identity {
			e.catalog.nextOID = ^ids.OID(0)
		} else {
			e.catalog.nextRevision = ids.DefinitionRevision(^uint64(0)) - 1
		}
		changes := &catalogChanges{}
		name := statement(t, "CREATE TEMP TABLE allocation_failure(i INT)").(*parser.CreateTable).Name
		ref, err := e.relationCreationNamespaceLocked(auth, name, "temporary", changes)
		e.mu.Unlock()
		op.Done()
		if err == nil || ref != (ids.ObjectRef{}) || e.catalog.state != base || s.TemporaryNamespace != (ids.ObjectAddress{}) || len(changes.Descriptors) != 0 || len(changes.Names) != 0 {
			t.Fatal("partial TEMP pair on second allocation failure", identity, ref, err, changes)
		}
		if namespaceUnitCapture(t, e, e.CatalogRead()).Count() != 4 {
			t.Fatal("failed pair published namespace")
		}
	}
}

func TestPGNamespaceTemporaryViewCreationAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE namespace_view_creator")
	roleExecute(t, e, op, "SET ROLE namespace_view_creator")
	roleExecute(t, e, op, "CREATE TEMP VIEW owned_temp AS SELECT 1 AS v")
	namespace := op.Session.TemporaryNamespace
	assertTemporaryNamespacePair(t, e, op.Session.SessionToken, namespace)
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "owned_temp"})
	view, ok := e.catalog.state.Relation(address)
	if !ok || view.Owner != e.Roles().LookupRef("namespace_view_creator") || view.Namespace() != namespace || view.RelationKind != catalog.ViewRelation || view.View == nil {
		t.Fatal("physical creator did not own staged TEMP view", view)
	}
	roleExecute(t, e, op, "SELECT v FROM owned_temp")
	roleExecute(t, e, op, "DROP VIEW owned_temp")
	assertTemporaryNamespacePair(t, e, op.Session.SessionToken, namespace)
}
