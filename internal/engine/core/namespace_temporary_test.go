package core_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func assertTemporaryNamespacePair(t *testing.T, e *core.Engine, token ids.SessionToken, table ids.ObjectAddress) ids.ObjectAddress {
	t.Helper()
	ns, ok := e.Catalog.State.Namespace(table)
	if !ok || ns.Temporary != token || ns.TemporaryToast || ns.Owner.ID() != 10 || ns.ACL.Explicit || len(ns.ACL.Entries) != 0 {
		t.Fatal("real table TEMP authority", ns)
	}
	suffix, ok := strings.CutPrefix(ns.Name(), "pg_temp_")
	if !ok {
		t.Fatal(ns.Name())
	}
	var toast ids.ObjectAddress
	count := 0
	for a, d := range e.Catalog.State.Descriptors() {
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
		if e.Catalog.State.NameAddress(catalog.DescriptorNameKey(other)) != a {
			t.Fatal("TEMP name identity", other)
		}
	}
	if count != 2 || toast == (ids.ObjectAddress{}) {
		t.Fatal("TEMP pair cardinality", count, toast)
	}
	return toast
}

func TestSequenceTemporaryNamespacePairAuthority(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE ROLE namespace_actor")
	sessionSQL(t, s, "ALTER DEFAULT PRIVILEGES FOR ROLE namespace_actor GRANT USAGE,CREATE ON SCHEMAS TO PUBLIC")
	sessionSQL(t, s, "SET ROLE namespace_actor")
	sessionSQL(t, s, "CREATE TEMP TABLE namespace_table(i INT)")
	table := s.TemporaryNamespace
	toast := assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	object := e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table, Name: "namespace_table"}))
	actor := e.Roles().LookupRef("namespace_actor")
	if object.Owner != actor || object.Owner.ID() == 10 {
		t.Fatal("namespace owner replaced object actor", object.Owner)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	for _, a := range []ids.ObjectAddress{table, toast} {
		ns := auth.Catalog.MustNamespace(a)
		rights, err := auth.Effective(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), a), false)
		want := authorization.Privileges(0)
		if a == table {
			want = authorization.UsagePrivilege | authorization.CreatePrivilege
		}
		if err != nil || rights != want || access.OwnTemporaryNamespace(core.TemporaryNamespace(auth), ns) != (a == table) {
			op.Done()
			t.Fatal("table versus toast authority", a, rights, err)
		}
	}
	auth.StagedTemporary = toast
	if core.TemporaryNamespace(auth) != table {
		op.Done()
		t.Fatal("toast impersonated active table namespace")
	}
	s.TemporaryNamespace = ids.ObjectAddress{}
	if core.TemporaryNamespace(auth) != (ids.ObjectAddress{}) {
		op.Done()
		t.Fatal("abandoned pointer regained table authority")
	}
	s.TemporaryNamespace = table
	op.Done()
	retained := namespaceUnitCapture(t, e, e.CatalogRead())
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.TemporaryNamespace != (ids.ObjectAddress{}) || e.Catalog.State.Get(object.Address()) != nil {
		t.Fatal("physical cleanup left active object authority")
	}
	assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	for _, id := range []ids.ObjectAddress{table, toast} {
		namespaceUnitRow(t, retained, uint32(id.ObjectID))
		namespaceUnitRow(t, namespaceUnitCapture(t, e, e.CatalogRead()), uint32(id.ObjectID))
	}
}

func TestSequenceTemporaryNamespacePairPrivate(t *testing.T) {
	e := engine.New()
	creator := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	for _, commit := range []bool{false, true} {
		sessionSQL(t, creator, "BEGIN")
		sessionSQL(t, creator, "CREATE TEMP TABLE namespace_private(i INT)")
		op, err := creator.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		auth, err := core.OperationAuthorization(op)
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
				if e.Catalog.State.Get(ns.Address()) != nil {
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
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP TABLE namespace_collation_table(i INT)")
	table := s.TemporaryNamespace
	toast := assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	sessionSQL(t, s, "CREATE COLLATION pg_temp.namespace_collation FROM \"C\"")
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: table, Name: "namespace_collation"})
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := core.EngineEndTemporaryCollations(e, s); err != nil {
		t.Fatal(err)
	}
	assertTemporaryNamespacePair(t, e, s.SessionToken, table)
	if e.Catalog.State.Get(address) != nil {
		t.Fatal("alternate cleanup retained collation child")
	}
	for _, d := range e.Catalog.State.Descriptors() {
		if d.Namespace() == table || d.Namespace() == toast {
			t.Fatal("alternate cleanup retained TEMP dependent", d)
		}
	}
}

func TestSequenceTemporaryNamespacePairAllocationFailure(t *testing.T) {
	for _, identity := range []bool{false, true} {
		e := engine.New()
		s := sessionStart(t, e, nil)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		auth, err := core.OperationAuthorization(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		base := e.Catalog.State
		e.Mu.Lock()
		if identity {
			e.Catalog.InstallOIDFrontierLocked(^ids.OID(0))
		} else {
			e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)) - 1)
		}
		changes := &session.Journal{}
		name := enginetest.Statement(t, "CREATE TEMP TABLE allocation_failure(i INT)").(*parser.CreateTable).Name
		ref, err := e.RelationCreationNamespaceLocked(auth, name, "temporary", changes)
		e.Mu.Unlock()
		op.Done()
		if err == nil || ref != (ids.ObjectRef{}) || e.Catalog.State != base || s.TemporaryNamespace != (ids.ObjectAddress{}) || len(changes.Descriptors) != 0 || len(changes.Names) != 0 {
			t.Fatal("partial TEMP pair on second allocation failure", identity, ref, err, changes)
		}
		if namespaceUnitCapture(t, e, e.CatalogRead()).Count() != 4 {
			t.Fatal("failed pair published namespace")
		}
	}
}

func TestPGNamespaceTemporaryViewCreationAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE ROLE namespace_view_creator")
	enginetest.RoleExecute(t, e, op, "SET ROLE namespace_view_creator")
	enginetest.RoleExecute(t, e, op, "CREATE TEMP VIEW owned_temp AS SELECT 1 AS v")
	namespace := op.Session.TemporaryNamespace
	assertTemporaryNamespacePair(t, e, op.Session.SessionToken, namespace)
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "owned_temp"})
	view, ok := e.Catalog.State.Relation(address)
	if !ok || view.Owner != e.Roles().LookupRef("namespace_view_creator") || view.Namespace() != namespace || view.RelationKind != catalog.ViewRelation || view.View == nil {
		t.Fatal("physical creator did not own staged TEMP view", view)
	}
	enginetest.RoleExecute(t, e, op, "SELECT v FROM owned_temp")
	enginetest.RoleExecute(t, e, op, "DROP VIEW owned_temp")
	assertTemporaryNamespacePair(t, e, op.Session.SessionToken, namespace)
}

func namespaceUnitCapture(t *testing.T, e *core.Engine, c catalog.Read) *exec.Source {
	t.Helper()
	d, ref := namespaceUnitSource(t, e, c)
	e.Mu.Lock()
	rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: c, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, c), Ref: ref, Source: d, Owner: &e.Catalog.Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}, capturePGNamespace)
	e.Mu.Unlock()
	if err != nil || rows == nil || rows.Ref != ref {
		t.Fatal(rows, err)
	}
	return rows
}

func namespaceUnitRow(t *testing.T, rows *exec.Source, id uint32) []sqltype.Value {
	t.Helper()
	var found []sqltype.Value
	for _, row := range rows.Anonymous {
		oid, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if oid == id {
			if found != nil {
				t.Fatal("duplicate namespace", id)
			}
			found = row
		}
	}
	if found == nil {
		t.Fatal("missing namespace", id)
	}
	return found
}

func namespaceUnitSource(t *testing.T, e *core.Engine, c catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2615}
	d, ok := c.Relation(a)
	if !ok {
		t.Fatal("missing namespace source")
	}
	return d, e.Catalog.Domain.Ref(c.Epoch(), a)
}

// Unit tests name producers by their former engine identifiers. Each one is
// resolved through the real pgcatalog registry, so a missing registration
// fails at package initialization instead of silently testing another path.
var (
	captureAvailableExtensions = registeredCatalogProducer(12081)
	captureDataTypePrivileges  = registeredCatalogProducer(16201)
	captureExtensionCatalog    = registeredCatalogProducer(1255)
	capturePGAM                = registeredCatalogProducer(2601)
	capturePGAggregate         = registeredCatalogProducer(2600)
	capturePGAttribute         = registeredCatalogProducer(1249)
	capturePGCast              = registeredCatalogProducer(2605)
	capturePGCollation         = registeredCatalogProducer(3456)
	capturePGConstraint        = registeredCatalogProducer(2606)
	capturePGCursors           = registeredCatalogProducer(12077)
	capturePGDatabase          = registeredCatalogProducer(1262)
	capturePGDefaultACL        = registeredCatalogProducer(826)
	capturePGDepend            = registeredCatalogProducer(2608)
	capturePGDescription       = registeredCatalogProducer(2609)
	capturePGEnum              = registeredCatalogProducer(3501)
	capturePGGroup             = registeredCatalogProducer(12008)
	capturePGIndexes           = registeredCatalogProducer(12002)
	capturePGNamespace         = registeredCatalogProducer(2615)
	capturePGOpclass           = registeredCatalogProducer(2616)
	capturePGOperator          = registeredCatalogProducer(2617)
	capturePGParameterACL      = registeredCatalogProducer(6243)
	capturePGRange             = registeredCatalogProducer(3541)
	capturePGRoles             = registeredCatalogProducer(12007)
	capturePGSequence          = registeredCatalogProducer(2224)
	capturePGSettings          = registeredCatalogProducer(16202)
	capturePGShDescription     = registeredCatalogProducer(2396)
	capturePGTablespace        = registeredCatalogProducer(1213)
	capturePGType              = registeredCatalogProducer(1247)
	capturePGUser              = registeredCatalogProducer(12014)
	capturePreparedSource      = registeredCatalogProducer(12001)
	captureTablePrivileges     = registeredCatalogProducer(16200)
	captureTableViewSummary    = registeredCatalogProducer(12000)
	captureTimezoneSource      = registeredCatalogProducer(12004)
)

func registeredCatalogProducer(oid ids.OID) pgcatalog.Producer {
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		panic(fmt.Sprintf("no catalog producer registered for OID %d", oid))
	}
	return produce
}
