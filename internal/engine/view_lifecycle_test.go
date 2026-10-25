package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestViewRecursiveLockCancellation(t *testing.T) {
	for _, route := range []struct {
		query   string
		prepare bool
	}{{"SELECT id FROM w", false}, {"SELECT id FROM w", true}, {"CREATE VIEW pending AS SELECT id FROM w", false}} {
		query, prepare := route.query, route.prepare
		t.Run(query+map[bool]string{false: "/execute", true: "/describe"}[prepare], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			reader := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE VIEW v AS SELECT id FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
				sessionSQL(t, holder, q)
			}
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			done := lockAsync(e, reader, ctx, query, prepare)
			lockPending(t, e, 1)
			cancel()
			lockAwait(t, done, context.Canceled)
			lockPending(t, e, 0)
			transactionEnd(t, holder, tx, false)
			if _, exists := e.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "pending"}); exists {
				t.Fatal("canceled view published")
			}
			lockEmpty(t, e)
		})
	}
}

func TestViewValidationReplacementAdmission(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	original := e.Catalog.Providers[ids.RelationClassID]
	var once sync.Once
	callbackRan, admissionReleased := false, false
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: original, callback: func() {
		once.Do(func() {
			callbackRan = true
			e.Catalog.Providers[ids.RelationClassID] = original
			admissionReleased = e.ReplacementMu.TryLock()
			if !admissionReleased {
				return
			}
			e.ReplacementMu.Unlock()
			if err := Reset(context.Background(), e); err != nil {
				t.Fatal(err)
			}
		})
	}}
	_, err := testExecute(e, context.Background(), statement(t, "CREATE VIEW v AS SELECT id FROM t"), nil)
	if !callbackRan || !admissionReleased {
		t.Fatal("view validation retains replacement read admission", callbackRan, admissionReleased)
	}
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	if _, exists := e.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "v"}); exists {
		t.Fatal("replacement-fenced view published")
	}
}

func TestViewValidationCancellationAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE sentinel(id INT)")
	before := e.CatalogRead().State()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := e.Catalog.Providers[ids.RelationClassID]
	var once sync.Once
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: original, callback: func() { once.Do(func() { e.Catalog.Providers[ids.RelationClassID] = original; cancel() }) }}
	_, err := testExecute(e, ctx, statement(t, "CREATE VIEW v AS SELECT id FROM sentinel"), nil)
	if !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before {
		t.Fatal(err, "partial catalog publication")
	}
	lockEmpty(t, e)
}

func TestViewCreateSequenceDependencyCancellation(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	reader := sessionStart(t, e, nil)
	for _, query := range []string{"CREATE SEQUENCE s", "CREATE TABLE t(id INT)"} {
		sessionSQL(t, holder, query)
	}
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "ALTER SEQUENCE s RENAME TO held")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := lockAsync(e, reader, ctx, "CREATE VIEW pending AS SELECT nextval('s') FROM t", false)
	lockPending(t, e, 1)
	cancel()
	lockAwait(t, done, context.Canceled)
	lockPending(t, e, 0)
	transactionEnd(t, holder, tx, false)
	lockEmpty(t, e)
}
