package core_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type namespaceUnreadCopyInput struct{ calls int }

func (r *namespaceUnreadCopyInput) Read([]byte) (int, error) { r.calls++; return 0, io.EOF }

func TestPGNamespaceCopyStoragePrecedence(t *testing.T) {
	e, owner, peer, foreign, _, persistent := namespaceTemporaryStorageFixture(t)
	name := e.CatalogRead().MustNamespace(foreign.Namespace()).Name()
	enginetest.SessionSQL(t, peer, "CREATE ROLE storage_copy_reader")
	deny := func(actor *session.Session, query, code, message string) {
		t.Helper()
		op, err := actor.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		before, sequence, oid, revision := e.Catalog.State, e.Visibility.CommitSeq(), e.Catalog.NextOID(), e.Catalog.NextRevision()
		store := e.Storage.Relations[foreign.Address()]
		root, ends := store.Versions, store.Ends
		persistentStore := e.Storage.Relations[persistent.Address()]
		persistentRoot, persistentEnds := persistentStore.Versions, persistentStore.Ends
		reader := &namespaceUnreadCopyInput{}
		var output bytes.Buffer
		count, err := scope.Copy(enginetest.Statement(t, query).(*parser.Copy), reader, &output)
		err = scope.Finish(err)
		op.Done()
		defaultUnitDiagnostic(t, err, code, message)
		if count != 0 || reader.calls != 0 || output.Len() != 0 || e.Catalog.State != before || e.Visibility.CommitSeq() != sequence || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.Relations[foreign.Address()] != store || !storage.SameVersionRoot(store.Versions, root) || store.Ends != ends || e.Storage.Relations[persistent.Address()] != persistentStore || !storage.SameVersionRoot(persistentStore.Versions, persistentRoot) || persistentStore.Ends != persistentEnds || peer.TemporaryNamespace != (ids.ObjectAddress{}) || peer.Transactions.AccessedTemporary {
			t.Fatal("denied COPY changed storage, frontier, input or TEMP footprint", query, count, err)
		}
		enginetest.SessionSQL(t, actor, "ROLLBACK")
	}
	enginetest.SessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY "+name+".storage_foreign FROM STDIN", "25006", "cannot execute COPY FROM in a read-only transaction")
	enginetest.SessionSQL(t, peer, "SET ROLE storage_copy_reader")
	enginetest.SessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY "+name+".storage_foreign FROM STDIN", "42501", "permission denied for schema "+name)
	enginetest.SessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY public.storage_parent FROM STDIN", "42501", "permission denied for table storage_parent")
	enginetest.SessionSQL(t, peer, "RESET ROLE")
	enginetest.SessionSQL(t, peer, "GRANT INSERT ON public.storage_parent TO storage_copy_reader")
	enginetest.SessionSQL(t, peer, "SET ROLE storage_copy_reader")
	enginetest.SessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY public.storage_parent FROM STDIN", "25006", "cannot execute COPY FROM in a read-only transaction")
	enginetest.SessionSQL(t, peer, "RESET ROLE")
	// Own TEMP storage remains a supported read-only destination.
	enginetest.SessionSQL(t, owner, "BEGIN READ ONLY")
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	count, err := scope.Copy(enginetest.Statement(t, "COPY storage_foreign FROM STDIN").(*parser.Copy), strings.NewReader("100\n"), nil)
	err = scope.Finish(err)
	op.Done()
	if err != nil || count != 1 {
		t.Fatal("own read-only COPY", count, err)
	}
	enginetest.SessionSQL(t, owner, "COMMIT")
	sequenceScalar(t, owner, "SELECT sum(v)::bigint FROM storage_foreign", 199)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
}

func TestPGNamespaceViewStoragePrecedence(t *testing.T) {
	e := engine.New()
	owner, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE ROLE storage_view_creator", "SET ROLE storage_view_creator",
		"CREATE TEMP TABLE storage_acl_source(v INT)", "INSERT INTO storage_acl_source VALUES(99)",
		"CREATE TEMP VIEW storage_acl_direct AS SELECT v FROM storage_acl_source",
		"CREATE TEMP VIEW storage_acl_nested AS SELECT v FROM storage_acl_direct",
		"CREATE TEMP VIEW storage_acl_outer_invoker WITH (security_invoker=true) AS SELECT v FROM storage_acl_direct",
		"CREATE TEMP VIEW storage_acl_invoker WITH (security_invoker=true) AS SELECT v FROM storage_acl_source",
		"CREATE TEMP VIEW storage_acl_outer_definer AS SELECT v FROM storage_acl_invoker",
	} {
		enginetest.SessionSQL(t, owner, query)
	}
	definers := []string{"storage_acl_direct", "storage_acl_nested", "storage_acl_outer_invoker"}
	invokers := []string{"storage_acl_invoker", "storage_acl_outer_definer"}
	for _, name := range append(append([]string(nil), definers...), invokers...) {
		enginetest.SessionSQL(t, owner, "GRANT SELECT ON "+name+" TO PUBLIC")
	}
	allowed := namespaceCapturedAuthorization(t, peer)
	catalog := e.CatalogRead()
	namespace := owner.TemporaryNamespace
	name := catalog.MustNamespace(namespace).Name()
	base := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "storage_acl_source"})
	store := e.Storage.Relations[base]
	root, ends := store.Versions, store.Ends
	enginetest.SessionSQL(t, owner, "REVOKE SELECT ON storage_acl_source FROM storage_view_creator")
	denied := namespaceCapturedAuthorization(t, peer)
	collect := func(auth engine.AuthorizationView, viewName, code, message string) {
		t.Helper()
		address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: viewName})
		relation := auth.Catalog.MustRelation(address)
		leaf := exec.Leaf{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), address), Relation: relation}
		before, sequence, oid, revision := e.Catalog.State, e.Visibility.CommitSeq(), e.Catalog.NextOID(), e.Catalog.NextRevision()
		e.Mu.Lock()
		refs, err := e.QueryTableRefsLocked(auth, auth.State.CurrentUser, []exec.Leaf{leaf}, nil, peer.SessionToken, plan.RowMark{})
		e.Mu.Unlock()
		defaultUnitDiagnostic(t, err, code, message)
		if len(refs) != 0 || e.Catalog.State != before || e.Visibility.CommitSeq() != sequence || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.Relations[base] != store || !storage.SameVersionRoot(store.Versions, root) || store.Ends != ends || peer.TemporaryNamespace != (ids.ObjectAddress{}) || peer.Transactions.AccessedTemporary {
			t.Fatal("view rejection changed physical frontier", viewName, refs, err)
		}
	}
	// An older paired snapshot retains actual source ACL authority: no live
	// authorization lookup may replace it with the newly revoked catalog.
	for _, view := range definers {
		collect(allowed, view, "0A000", "cannot access temporary tables of other sessions")
		collect(denied, view, "42501", "permission denied for table storage_acl_source")
		query := "SELECT v FROM " + name + "." + view
		ast, description := sequencePreparedDescription(t, e, peer, query)
		for _, prepared := range []bool{false, true} {
			before, sequence, oid, revision := e.Catalog.State, e.Visibility.CommitSeq(), e.Catalog.NextOID(), e.Catalog.NextRevision()
			var err error
			if prepared {
				_, err = sequencePreparedExecute(e, peer, ast, &description)
			} else {
				op, acquireErr := peer.Acquire(context.Background())
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				_, err = e.Execute(op, ast, 0, nil)
				op.Done()
			}
			defaultUnitDiagnostic(t, err, "42501", "permission denied for table storage_acl_source")
			if e.Catalog.State != before || e.Visibility.CommitSeq() != sequence || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.Relations[base] != store || !storage.SameVersionRoot(store.Versions, root) || store.Ends != ends || peer.TemporaryNamespace != (ids.ObjectAddress{}) || peer.Transactions.AccessedTemporary {
				t.Fatal("denied direct/prepared view changed storage or TEMP footprint", view, prepared, err)
			}
		}
	}
	// A nested invoker always receives the original bootstrap invoker, even
	// below an ordinary definer; the physical boundary remains independent.
	for _, view := range invokers {
		collect(denied, view, "0A000", "cannot access temporary tables of other sessions")
	}
	enginetest.SessionSQL(t, owner, "GRANT SELECT ON storage_acl_source TO storage_view_creator")
	restored := namespaceCapturedAuthorization(t, peer)
	for _, view := range definers {
		collect(restored, view, "0A000", "cannot access temporary tables of other sessions")
		sequenceScalar(t, owner, "SELECT v FROM "+view, 99)
	}
	sequenceScalar(t, owner, "SELECT v FROM storage_acl_source", 99)
	assertTemporaryNamespacePair(t, e, owner.SessionToken, namespace)
}

func defaultUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" {
		t.Fatalf("diagnostic = %#v; want %s %q", err, code, message)
	}
}

func assertTemporaryNamespacePair(t *testing.T, e *engine.Engine, token ids.SessionToken, table ids.ObjectAddress) ids.ObjectAddress {
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
