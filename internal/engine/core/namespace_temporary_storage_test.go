package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
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

func namespaceTemporaryStorageFixture(t *testing.T) (*engine.Engine, *session.Session, *session.Session, *catalog.Relation, *catalog.Relation, *catalog.Relation) {
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
	assertTemporaryNamespacePair(t, e, owner.SessionToken, namespace)
	lookup := func(namespace ids.ObjectAddress, name string) *catalog.Relation {
		address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
		return view.MustRelation(address)
	}
	return e, owner, peer, lookup(namespace, "storage_foreign"), lookup(namespace, "storage_chain"), lookup(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "storage_parent")
}

func namespaceCapturedAuthorization(t *testing.T, s *session.Session) engine.AuthorizationView {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	auth, err := engine.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestPGNamespaceForeignTemporaryStorageCapture(t *testing.T) {
	e, owner, peer, foreign, chain, persistent := namespaceTemporaryStorageFixture(t)
	catalogView := e.CatalogRead()
	ownerAuth, peerAuth := namespaceCapturedAuthorization(t, owner), namespaceCapturedAuthorization(t, peer)
	collect := func(view catalog.Read, relation *catalog.Relation, only, analysis bool, token ids.SessionToken) ([]ids.ObjectRef, error) {
		t.Helper()
		leaf := exec.Leaf{Ref: e.Catalog.Domain.Ref(view.Epoch(), relation.Address()), Relation: relation, Only: only, AnalysisOnly: analysis}
		auth := peerAuth
		if token == owner.SessionToken {
			auth = ownerAuth
		}
		auth.Recapture(view)
		e.Mu.Lock()
		refs, err := e.QueryTableRefsLocked(auth, auth.State.CurrentUser, []exec.Leaf{leaf}, nil, token, plan.RowMark{})
		e.Mu.Unlock()
		return refs, err
	}
	before, sequence, oid, revision := e.Catalog.State, e.Visibility.CommitSeq(), e.Catalog.NextOID(), e.Catalog.NextRevision()
	for _, tc := range []struct {
		relation *catalog.Relation
		only     bool
	}{{foreign, false}, {foreign, true}, {chain, false}} {
		refs, err := collect(catalogView, tc.relation, tc.only, false, peer.SessionToken)
		defaultUnitDiagnostic(t, err, "0A000", "cannot access temporary tables of other sessions")
		if len(refs) != 0 || e.Catalog.State != before || e.Visibility.CommitSeq() != sequence || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || peer.TemporaryNamespace != (ids.ObjectAddress{}) || peer.Transactions.AccessedTemporary {
			t.Fatal("rejected physical source changed capture frontier or TEMP footprint", refs, err)
		}
	}
	e.Mu.Lock()
	seeded, seededErr := e.QueryTableRefsLocked(peerAuth, peerAuth.State.CurrentUser, nil, []ids.ObjectRef{e.Catalog.Domain.Ref(catalogView.Epoch(), foreign.Address())}, peer.SessionToken, plan.RowMark{})
	e.Mu.Unlock()
	defaultUnitDiagnostic(t, seededErr, "0A000", "cannot access temporary tables of other sessions")
	if len(seeded) != 0 {
		t.Fatal("preseeded target bypassed physical admission", seeded)
	}
	refs, err := collect(catalogView, foreign, false, true, peer.SessionToken)
	if err != nil || len(refs) != 0 {
		t.Fatal("metadata-only leaf admitted physical storage", refs, err)
	}
	refs, err = collect(catalogView, foreign, false, false, owner.SessionToken)
	if err != nil || len(refs) != 1 || refs[0].Address != foreign.Address() {
		t.Fatal("own TEMP storage denied", refs, err)
	}
	refs, err = collect(catalogView, persistent, false, false, peer.SessionToken)
	if err != nil || len(refs) != 1 || refs[0].Address != persistent.Address() {
		t.Fatal("persistent ancestor did not skip foreign TEMP child", refs, err)
	}
	refs, err = collect(catalogView, persistent, false, false, owner.SessionToken)
	if err != nil || len(refs) != 2 {
		t.Fatal("own inherited TEMP child omitted", refs, err)
	}
	missing := catalog.CloneState(catalogView.State())
	missing.RemoveDescriptor(foreign.Namespace())
	_, err = collect(e.Catalog.Read(missing), foreign, true, false, owner.SessionToken)
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("missing captured namespace accepted", err)
	}
}

func TestPGNamespaceForeignTemporaryStorageExecution(t *testing.T) {
	e, owner, peer, foreign, chain, _ := namespaceTemporaryStorageFixture(t)
	name := e.CatalogRead().Get(foreign.Namespace()).Name()
	sequenceScalar(t, owner, "SELECT v FROM storage_foreign", 99)
	sequenceScalar(t, owner, "SELECT v FROM storage_chain", 99)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
	sequenceScalar(t, peer, "SELECT '"+name+".storage_foreign'::regclass::oid::bigint", int64(foreign.Address().ObjectID))
	for _, query := range []string{"SELECT v FROM " + name + ".storage_foreign", "SELECT v FROM ONLY " + name + ".storage_foreign", "SELECT v FROM " + name + "." + chain.Name()} {
		ast, description := sequencePreparedDescription(t, e, peer, query)
		before, sequence, oid, revision := e.Catalog.State, e.Visibility.CommitSeq(), e.Catalog.NextOID(), e.Catalog.NextRevision()
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
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.Catalog.State != before || e.Visibility.CommitSeq() != sequence || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || peer.TemporaryNamespace != (ids.ObjectAddress{}) || peer.Transactions.AccessedTemporary {
				t.Fatal("foreign physical execution boundary/atomicity", prepared, err)
			}
		}
	}
	sequenceScalar(t, owner, "SELECT v FROM storage_foreign", 99)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
}

func TestPGNamespaceForeignTemporaryStorageTargets(t *testing.T) {
	e, owner, peer, foreign, _, _ := namespaceTemporaryStorageFixture(t)
	name := e.CatalogRead().Get(foreign.Namespace()).Name()
	store := e.Storage.Relations[foreign.Address()]
	root, ends := store.Versions, store.Ends
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
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.Catalog.State != before || e.Visibility.CommitSeq() != sequence || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.Relations[foreign.Address()] != store || !storage.SameVersionRoot(store.Versions, root) || store.Ends != ends || peer.Transactions.AccessedTemporary {
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
		before, sequence, oid, revision := e.Catalog.State, e.Visibility.CommitSeq(), e.Catalog.NextOID(), e.Catalog.NextRevision()
		count, err := scope.Copy(enginetest.Statement(t, query).(*parser.Copy), strings.NewReader("100\n"), &output)
		err = scope.Finish(err)
		op.Done()
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions"}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || count != 0 || output.Len() != 0 || e.Catalog.State != before || e.Visibility.CommitSeq() != sequence || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.Relations[foreign.Address()] != store || !storage.SameVersionRoot(store.Versions, root) || store.Ends != ends || peer.Transactions.AccessedTemporary {
			t.Fatal("foreign COPY changed owner state or emitted rows", query, count, err)
		}
	}
	sequenceScalar(t, owner, "SELECT v FROM storage_foreign", 99)
	sequenceScalar(t, peer, "SELECT v FROM public.storage_parent", 7)
}

func TestPGNamespaceOwnAndPersistentStorageTargets(t *testing.T) {
	e := engine.New()
	owner, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TEMP TABLE storage_positive(v INT)")
	enginetest.SessionSQL(t, peer, "CREATE TABLE public.storage_positive(v INT)")
	for _, tc := range []struct {
		session *session.Session
		table   string
	}{{owner, "pg_temp.storage_positive"}, {peer, "public.storage_positive"}} {
		for _, query := range []string{"INSERT INTO " + tc.table + " VALUES(1)", "UPDATE " + tc.table + " SET v=2", "DELETE FROM " + tc.table} {
			if result := enginetest.SessionSQL(t, tc.session, query); result.Count != 1 {
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
		count, err := scope.Copy(enginetest.Statement(t, "COPY "+tc.table+" FROM STDIN").(*parser.Copy), strings.NewReader("7\n"), nil)
		err = scope.Finish(err)
		op.Done()
		if err != nil || count != 1 {
			t.Fatal("own/persistent COPY FROM", tc.table, count, err)
		}
		sequenceScalar(t, tc.session, "SELECT v FROM "+tc.table, 7)
		enginetest.SessionSQL(t, tc.session, "TRUNCATE "+tc.table)
		sequenceScalar(t, tc.session, "SELECT count(*) FROM "+tc.table, 0)
	}
}
