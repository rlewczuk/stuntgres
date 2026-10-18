package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func namespaceTemporaryStorageFixture(t *testing.T) (*Engine, *Session, *Session, *relationDescriptor, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
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
		sessionSQL(t, owner, q)
	}
	catalog := e.CatalogRead()
	namespace := owner.temporaryNamespace
	assertTemporaryNamespacePair(t, e, owner.token, namespace)
	lookup := func(namespace ids.ObjectAddress, name string) *relationDescriptor {
		address := catalog.state.names[ids.NameKey{ClassID: RelationClassID, Namespace: namespace, Name: name}]
		return catalog.state.descriptors[address].(*relationDescriptor)
	}
	return e, owner, peer, lookup(namespace, "storage_foreign"), lookup(namespace, "storage_chain"), lookup(ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, "storage_parent")
}

func namespaceCapturedAuthorization(t *testing.T, s *Session) AuthorizationView {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestPGNamespaceForeignTemporaryStorageCapture(t *testing.T) {
	e, owner, peer, foreign, chain, persistent := namespaceTemporaryStorageFixture(t)
	catalog := e.CatalogRead()
	ownerAuth, peerAuth := namespaceCapturedAuthorization(t, owner), namespaceCapturedAuthorization(t, peer)
	collect := func(view CatalogRead, relation *relationDescriptor, only, analysis bool, token SessionToken) ([]ids.ObjectRef, error) {
		t.Helper()
		leaf := queryLeaf{ref: e.domain.Ref(view.Epoch(), relation.address), relation: relation, only: only, analysisOnly: analysis}
		auth := peerAuth
		if token == owner.token {
			auth = ownerAuth
		}
		auth.recaptureCatalog(view)
		e.mu.Lock()
		refs, err := e.queryTableRefsLocked(auth, auth.state.CurrentUser, []queryLeaf{leaf}, nil, token, queryRowMark{})
		e.mu.Unlock()
		return refs, err
	}
	before, sequence, oid, revision := e.state, e.commitSeq, e.nextOID, e.nextRevision
	for _, tc := range []struct {
		relation *relationDescriptor
		only     bool
	}{{foreign, false}, {foreign, true}, {chain, false}} {
		refs, err := collect(catalog, tc.relation, tc.only, false, peer.token)
		defaultUnitDiagnostic(t, err, "0A000", "cannot access temporary tables of other sessions")
		if len(refs) != 0 || e.state != before || e.commitSeq != sequence || e.nextOID != oid || e.nextRevision != revision || peer.temporaryNamespace != (ids.ObjectAddress{}) || peer.transactions.accessedTemporary {
			t.Fatal("rejected physical source changed capture frontier or TEMP footprint", refs, err)
		}
	}
	e.mu.Lock()
	seeded, seededErr := e.queryTableRefsLocked(peerAuth, peerAuth.state.CurrentUser, nil, []ids.ObjectRef{e.domain.Ref(catalog.Epoch(), foreign.address)}, peer.token, queryRowMark{})
	e.mu.Unlock()
	defaultUnitDiagnostic(t, seededErr, "0A000", "cannot access temporary tables of other sessions")
	if len(seeded) != 0 {
		t.Fatal("preseeded target bypassed physical admission", seeded)
	}
	refs, err := collect(catalog, foreign, false, true, peer.token)
	if err != nil || len(refs) != 0 {
		t.Fatal("metadata-only leaf admitted physical storage", refs, err)
	}
	refs, err = collect(catalog, foreign, false, false, owner.token)
	if err != nil || len(refs) != 1 || refs[0].Address != foreign.address {
		t.Fatal("own TEMP storage denied", refs, err)
	}
	refs, err = collect(catalog, persistent, false, false, peer.token)
	if err != nil || len(refs) != 1 || refs[0].Address != persistent.address {
		t.Fatal("persistent ancestor did not skip foreign TEMP child", refs, err)
	}
	refs, err = collect(catalog, persistent, false, false, owner.token)
	if err != nil || len(refs) != 2 {
		t.Fatal("own inherited TEMP child omitted", refs, err)
	}
	missing := cloneCatalog(catalog.state)
	delete(missing.descriptors, foreign.namespace)
	_, err = collect(CatalogRead{owner: e, state: missing}, foreign, true, false, owner.token)
	if !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("missing captured namespace accepted", err)
	}
}

func TestPGNamespaceForeignTemporaryStorageExecution(t *testing.T) {
	e, owner, peer, foreign, chain, _ := namespaceTemporaryStorageFixture(t)
	name := e.CatalogRead().state.descriptors[foreign.namespace].Name()
	sequenceScalar(t, owner, "SELECT v FROM storage_foreign", 99)
	sequenceScalar(t, owner, "SELECT v FROM storage_chain", 99)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
	sequenceScalar(t, peer, "SELECT '"+name+".storage_foreign'::regclass::oid::bigint", int64(foreign.address.ObjectID))
	for _, query := range []string{"SELECT v FROM " + name + ".storage_foreign", "SELECT v FROM ONLY " + name + ".storage_foreign", "SELECT v FROM " + name + "." + chain.name} {
		ast, description := sequencePreparedDescription(t, e, peer, query)
		before, sequence, oid, revision := e.state, e.commitSeq, e.nextOID, e.nextRevision
		for _, prepared := range []bool{false, true} {
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
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.state != before || e.commitSeq != sequence || e.nextOID != oid || e.nextRevision != revision || peer.temporaryNamespace != (ids.ObjectAddress{}) || peer.transactions.accessedTemporary {
				t.Fatal("foreign physical execution boundary/atomicity", prepared, err)
			}
		}
	}
	sequenceScalar(t, owner, "SELECT v FROM storage_foreign", 99)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
}

func TestPGNamespaceForeignTemporaryStorageTargets(t *testing.T) {
	e, owner, peer, foreign, _, _ := namespaceTemporaryStorageFixture(t)
	name := e.CatalogRead().state.descriptors[foreign.namespace].Name()
	store := e.storage[foreign.address]
	root, ends := store.versions, store.ends
	queries := []string{
		"INSERT INTO " + name + ".storage_foreign VALUES(100)",
		"UPDATE " + name + ".storage_foreign SET v=100",
		"DELETE FROM " + name + ".storage_foreign",
		"INSERT INTO " + name + ".storage_view VALUES(100)",
		"UPDATE " + name + ".storage_view SET v=100",
		"DELETE FROM " + name + ".storage_view",
		"UPDATE " + name + ".storage_foreign SET v=100 FROM public.storage_parent",
		"DELETE FROM " + name + ".storage_foreign USING public.storage_parent",
		"MERGE INTO " + name + ".storage_foreign AS t USING public.storage_parent AS s ON t.v=s.v WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)",
		"TRUNCATE " + name + ".storage_foreign",
	}
	for _, query := range queries {
		ast, description := sequencePreparedDescription(t, e, peer, query)
		for _, prepared := range []bool{false, true} {
			before, sequence, oid, revision := e.state, e.commitSeq, e.nextOID, e.nextRevision
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
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.state != before || e.commitSeq != sequence || e.nextOID != oid || e.nextRevision != revision || e.storage[foreign.address] != store || !sameVersionRoot(store.versions, root) || store.ends != ends || peer.transactions.accessedTemporary {
				t.Fatal("foreign target changed owner state or rejection boundary", query, prepared, err)
			}
		}
	}
	for _, query := range []string{"COPY " + name + ".storage_foreign FROM STDIN", "COPY " + name + ".storage_foreign TO STDOUT"} {
		op, err := peer.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		var output bytes.Buffer
		before, sequence, oid, revision := e.state, e.commitSeq, e.nextOID, e.nextRevision
		count, err := scope.Copy(statement(t, query).(*parser.Copy), strings.NewReader("100\n"), &output)
		err = scope.Finish(err)
		op.Done()
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions"}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || count != 0 || output.Len() != 0 || e.state != before || e.commitSeq != sequence || e.nextOID != oid || e.nextRevision != revision || e.storage[foreign.address] != store || !sameVersionRoot(store.versions, root) || store.ends != ends || peer.transactions.accessedTemporary {
			t.Fatal("foreign COPY changed owner state or emitted rows", query, count, err)
		}
	}
	sequenceScalar(t, owner, "SELECT v FROM storage_foreign", 99)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
}

func TestPGNamespaceOwnAndPersistentStorageTargets(t *testing.T) {
	e := New()
	owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TEMP TABLE storage_positive(v INT)")
	sessionSQL(t, peer, "CREATE TABLE public.storage_positive(v INT)")
	for _, tc := range []struct {
		session *Session
		table   string
	}{{owner, "pg_temp.storage_positive"}, {peer, "public.storage_positive"}} {
		for _, query := range []string{"INSERT INTO " + tc.table + " VALUES(1)", "UPDATE " + tc.table + " SET v=2", "DELETE FROM " + tc.table} {
			if result := sessionSQL(t, tc.session, query); result.Count != 1 {
				t.Fatal("supported physical mutation changed count", query, result.Count)
			}
		}
		op, err := tc.session.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		count, err := scope.Copy(statement(t, "COPY "+tc.table+" FROM STDIN").(*parser.Copy), strings.NewReader("7\n"), nil)
		err = scope.Finish(err)
		op.Done()
		if err != nil || count != 1 {
			t.Fatal("own/persistent COPY FROM", tc.table, count, err)
		}
		sequenceScalar(t, tc.session, "SELECT v FROM "+tc.table, 7)
		sessionSQL(t, tc.session, "TRUNCATE "+tc.table)
		sequenceScalar(t, tc.session, "SELECT count(*) FROM "+tc.table, 0)
	}
}
