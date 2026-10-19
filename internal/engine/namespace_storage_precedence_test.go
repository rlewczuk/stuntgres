package engine

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type namespaceUnreadCopyInput struct{ calls int }

func (r *namespaceUnreadCopyInput) Read([]byte) (int, error) { r.calls++; return 0, io.EOF }

func TestPGNamespaceCopyStoragePrecedence(t *testing.T) {
	e, owner, peer, foreign, _, persistent := namespaceTemporaryStorageFixture(t)
	name := e.CatalogRead().MustNamespace(foreign.Namespace()).Name()
	sessionSQL(t, peer, "CREATE ROLE storage_copy_reader")
	deny := func(actor *Session, query, code, message string) {
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
		before, sequence, oid, revision := e.catalog.state, e.visibility.commitSeq, e.catalog.nextOID, e.catalog.nextRevision
		store := e.storage.Relations[foreign.Address()]
		root, ends := store.Versions, store.Ends
		persistentStore := e.storage.Relations[persistent.Address()]
		persistentRoot, persistentEnds := persistentStore.Versions, persistentStore.Ends
		reader := &namespaceUnreadCopyInput{}
		var output bytes.Buffer
		count, err := scope.Copy(statement(t, query).(*parser.Copy), reader, &output)
		err = scope.Finish(err)
		op.Done()
		defaultUnitDiagnostic(t, err, code, message)
		if count != 0 || reader.calls != 0 || output.Len() != 0 || e.catalog.state != before || e.visibility.commitSeq != sequence || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage.Relations[foreign.Address()] != store || !sameVersionRoot(store.Versions, root) || store.Ends != ends || e.storage.Relations[persistent.Address()] != persistentStore || !sameVersionRoot(persistentStore.Versions, persistentRoot) || persistentStore.Ends != persistentEnds || peer.temporaryNamespace != (ids.ObjectAddress{}) || peer.transactions.accessedTemporary {
			t.Fatal("denied COPY changed storage, frontier, input or TEMP footprint", query, count, err)
		}
		sessionSQL(t, actor, "ROLLBACK")
	}
	sessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY "+name+".storage_foreign FROM STDIN", "25006", "cannot execute COPY FROM in a read-only transaction")
	sessionSQL(t, peer, "SET ROLE storage_copy_reader")
	sessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY "+name+".storage_foreign FROM STDIN", "42501", "permission denied for schema "+name)
	sessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY public.storage_parent FROM STDIN", "42501", "permission denied for table storage_parent")
	sessionSQL(t, peer, "RESET ROLE")
	sessionSQL(t, peer, "GRANT INSERT ON public.storage_parent TO storage_copy_reader")
	sessionSQL(t, peer, "SET ROLE storage_copy_reader")
	sessionSQL(t, peer, "BEGIN READ ONLY")
	deny(peer, "COPY public.storage_parent FROM STDIN", "25006", "cannot execute COPY FROM in a read-only transaction")
	sessionSQL(t, peer, "RESET ROLE")
	// Own TEMP storage remains a supported read-only destination.
	sessionSQL(t, owner, "BEGIN READ ONLY")
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	count, err := scope.Copy(statement(t, "COPY storage_foreign FROM STDIN").(*parser.Copy), strings.NewReader("100\n"), nil)
	err = scope.Finish(err)
	op.Done()
	if err != nil || count != 1 {
		t.Fatal("own read-only COPY", count, err)
	}
	sessionSQL(t, owner, "COMMIT")
	sequenceScalar(t, owner, "SELECT sum(v)::bigint FROM storage_foreign", 199)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
}

func TestPGNamespaceViewStoragePrecedence(t *testing.T) {
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, query := range []string{
		"CREATE ROLE storage_view_creator", "SET ROLE storage_view_creator",
		"CREATE TEMP TABLE storage_acl_source(v INT)", "INSERT INTO storage_acl_source VALUES(99)",
		"CREATE TEMP VIEW storage_acl_direct AS SELECT v FROM storage_acl_source",
		"CREATE TEMP VIEW storage_acl_nested AS SELECT v FROM storage_acl_direct",
		"CREATE TEMP VIEW storage_acl_outer_invoker WITH (security_invoker=true) AS SELECT v FROM storage_acl_direct",
		"CREATE TEMP VIEW storage_acl_invoker WITH (security_invoker=true) AS SELECT v FROM storage_acl_source",
		"CREATE TEMP VIEW storage_acl_outer_definer AS SELECT v FROM storage_acl_invoker",
	} {
		sessionSQL(t, owner, query)
	}
	definers := []string{"storage_acl_direct", "storage_acl_nested", "storage_acl_outer_invoker"}
	invokers := []string{"storage_acl_invoker", "storage_acl_outer_definer"}
	for _, name := range append(append([]string(nil), definers...), invokers...) {
		sessionSQL(t, owner, "GRANT SELECT ON "+name+" TO PUBLIC")
	}
	allowed := namespaceCapturedAuthorization(t, peer)
	catalog := e.CatalogRead()
	namespace := owner.temporaryNamespace
	name := catalog.MustNamespace(namespace).Name()
	base := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "storage_acl_source"})
	store := e.storage.Relations[base]
	root, ends := store.Versions, store.Ends
	sessionSQL(t, owner, "REVOKE SELECT ON storage_acl_source FROM storage_view_creator")
	denied := namespaceCapturedAuthorization(t, peer)
	collect := func(auth AuthorizationView, viewName, code, message string) {
		t.Helper()
		address := auth.catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: viewName})
		relation := auth.catalog.MustRelation(address)
		leaf := queryLeaf{ref: e.catalog.Domain.Ref(auth.catalog.Epoch(), address), relation: relation}
		before, sequence, oid, revision := e.catalog.state, e.visibility.commitSeq, e.catalog.nextOID, e.catalog.nextRevision
		e.mu.Lock()
		refs, err := e.queryTableRefsLocked(auth, auth.state.CurrentUser, []queryLeaf{leaf}, nil, peer.token, queryRowMark{})
		e.mu.Unlock()
		defaultUnitDiagnostic(t, err, code, message)
		if len(refs) != 0 || e.catalog.state != before || e.visibility.commitSeq != sequence || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage.Relations[base] != store || !sameVersionRoot(store.Versions, root) || store.Ends != ends || peer.temporaryNamespace != (ids.ObjectAddress{}) || peer.transactions.accessedTemporary {
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
			before, sequence, oid, revision := e.catalog.state, e.visibility.commitSeq, e.catalog.nextOID, e.catalog.nextRevision
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
			if e.catalog.state != before || e.visibility.commitSeq != sequence || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage.Relations[base] != store || !sameVersionRoot(store.Versions, root) || store.Ends != ends || peer.temporaryNamespace != (ids.ObjectAddress{}) || peer.transactions.accessedTemporary {
				t.Fatal("denied direct/prepared view changed storage or TEMP footprint", view, prepared, err)
			}
		}
	}
	// A nested invoker always receives the original bootstrap invoker, even
	// below an ordinary definer; the physical boundary remains independent.
	for _, view := range invokers {
		collect(denied, view, "0A000", "cannot access temporary tables of other sessions")
	}
	sessionSQL(t, owner, "GRANT SELECT ON storage_acl_source TO storage_view_creator")
	restored := namespaceCapturedAuthorization(t, peer)
	for _, view := range definers {
		collect(restored, view, "0A000", "cannot access temporary tables of other sessions")
		sequenceScalar(t, owner, "SELECT v FROM "+view, 99)
	}
	sequenceScalar(t, owner, "SELECT v FROM storage_acl_source", 99)
	assertTemporaryNamespacePair(t, e, owner.token, namespace)
}
