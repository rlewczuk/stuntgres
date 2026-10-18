package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func assertTemporaryNamespacePair(t *testing.T, e *Engine, token SessionToken, table ids.ObjectAddress) ids.ObjectAddress {
	t.Helper()
	ns, ok := e.state.descriptors[table].(*namespaceDescriptor)
	if !ok || ns.temporary != token || ns.temporaryToast || ns.owner.ID() != 10 || ns.acl.Explicit || len(ns.acl.Entries) != 0 {
		t.Fatal("real table TEMP authority", ns)
	}
	suffix, ok := strings.CutPrefix(ns.name, "pg_temp_")
	if !ok {
		t.Fatal(ns.name)
	}
	var toast ids.ObjectAddress
	count := 0
	for a, d := range e.state.descriptors {
		other, ok := d.(*namespaceDescriptor)
		if !ok || other.temporary != token {
			continue
		}
		count++
		if other.temporaryToast {
			if other.name != "pg_toast_temp_"+suffix || other.owner != ns.owner || other.acl.Explicit || len(other.acl.Entries) != 0 || other.address == table {
				t.Fatal("real toast TEMP authority", other)
			}
			toast = a
		}
		if e.state.names[descriptorNameKey(other)] != a {
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
	table := s.temporaryNamespace
	toast := assertTemporaryNamespacePair(t, e, s.token, table)
	object := e.state.descriptors[e.state.names[ids.NameKey{ClassID: RelationClassID, Namespace: table, Name: "namespace_table"}]].(*relationDescriptor)
	actor := e.Roles().LookupRef("namespace_actor")
	if object.owner != actor || object.owner.ID() == 10 {
		t.Fatal("namespace owner replaced object actor", object.owner)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	auth, err := op.Authorization()
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	for _, a := range []ids.ObjectAddress{table, toast} {
		ns := auth.catalog.state.descriptors[a].(*namespaceDescriptor)
		rights, err := auth.Effective(e.domain.Ref(auth.catalog.Epoch(), a), false)
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
	s.temporaryNamespace = ids.ObjectAddress{}
	if temporaryNamespace(auth) != (ids.ObjectAddress{}) {
		op.Done()
		t.Fatal("abandoned pointer regained table authority")
	}
	s.temporaryNamespace = table
	op.Done()
	retained := namespaceUnitCapture(t, e, e.CatalogRead())
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.temporaryNamespace != (ids.ObjectAddress{}) || e.state.descriptors[object.address] != nil {
		t.Fatal("physical cleanup left active object authority")
	}
	assertTemporaryNamespacePair(t, e, s.token, table)
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
		auth, err := op.Authorization()
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		private := namespaceUnitCapture(t, e, auth.catalog)
		committed := namespaceUnitCapture(t, e, e.CatalogRead())
		count := 0
		for _, d := range auth.catalog.state.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == creator.token {
				count++
				namespaceUnitRow(t, private, uint32(ns.address.ObjectID))
				if e.state.descriptors[ns.address] != nil {
					op.Done()
					t.Fatal("uncommitted pair leaked")
				}
			}
		}
		if count != 2 || committed.count() != 4 {
			op.Done()
			t.Fatal("private paired inventory", count, committed.count())
		}
		op.Done()
		if commit {
			sessionSQL(t, creator, "COMMIT")
			assertTemporaryNamespacePair(t, e, creator.token, creator.temporaryNamespace)
		} else {
			sessionSQL(t, creator, "ROLLBACK")
			if namespaceUnitCapture(t, e, e.CatalogRead()).count() != 4 {
				t.Fatal("rollback published TEMP pair")
			}
		}
	}
	if peer.temporaryNamespace != (ids.ObjectAddress{}) {
		t.Fatal("peer acquired creator table namespace")
	}
}

func TestTemporaryNamespaceCollationRetirement(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP TABLE namespace_collation_table(i INT)")
	table := s.temporaryNamespace
	toast := assertTemporaryNamespacePair(t, e, s.token, table)
	sessionSQL(t, s, "CREATE COLLATION pg_temp.namespace_collation FROM \"C\"")
	address := e.state.names[ids.NameKey{ClassID: CollationClassID, Namespace: table, Name: "namespace_collation"}]
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.endTemporaryCollations(s); err != nil {
		t.Fatal(err)
	}
	assertTemporaryNamespacePair(t, e, s.token, table)
	if e.state.descriptors[address] != nil {
		t.Fatal("alternate cleanup retained collation child")
	}
	for _, d := range e.state.descriptors {
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
		auth, err := op.Authorization()
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		base := e.state
		e.mu.Lock()
		if identity {
			e.nextOID = ^ids.OID(0)
		} else {
			e.nextRevision = ids.DefinitionRevision(^uint64(0)) - 1
		}
		changes := &catalogChanges{}
		name := statement(t, "CREATE TEMP TABLE allocation_failure(i INT)").(*parser.CreateTable).Name
		ref, err := e.relationCreationNamespaceLocked(auth, name, "temporary", changes)
		e.mu.Unlock()
		op.Done()
		if err == nil || ref != (ids.ObjectRef{}) || e.state != base || s.temporaryNamespace != (ids.ObjectAddress{}) || len(changes.descriptors) != 0 || len(changes.names) != 0 {
			t.Fatal("partial TEMP pair on second allocation failure", identity, ref, err, changes)
		}
		if namespaceUnitCapture(t, e, e.CatalogRead()).count() != 4 {
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
	namespace := op.session.temporaryNamespace
	assertTemporaryNamespacePair(t, e, op.session.token, namespace)
	address := e.state.names[ids.NameKey{ClassID: RelationClassID, Namespace: namespace, Name: "owned_temp"}]
	view, ok := e.state.descriptors[address].(*relationDescriptor)
	if !ok || view.owner != e.Roles().LookupRef("namespace_view_creator") || view.namespace != namespace || view.relationKind != viewRelation || view.view == nil {
		t.Fatal("physical creator did not own staged TEMP view", view)
	}
	roleExecute(t, e, op, "SELECT v FROM owned_temp")
	roleExecute(t, e, op, "DROP VIEW owned_temp")
	assertTemporaryNamespacePair(t, e, op.session.token, namespace)
}
