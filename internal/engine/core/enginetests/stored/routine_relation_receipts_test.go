package stored_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"testing"
)

func TestRoutineScalarRelationPublicationReceipts(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE FUNCTION receipt_value() RETURNS integer RETURN 5", "CREATE TABLE receipt_rows(v integer DEFAULT receipt_value())"} {
		enginetest.SessionSQL(t, s, q)
	}
	source := e.CatalogRead()
	original := transformStoredRelation(t, source, "receipt_rows")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION receipt_value() RETURNS integer RETURN 9")
	firstView := e.CatalogRead()
	first := transformStoredRelation(t, firstView, "receipt_rows")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION receipt_value() RETURNS integer RETURN 11")
	view := e.CatalogRead()
	current := transformStoredRelation(t, view, "receipt_rows")
	if !stored.ScalarRelationSuccessor(view, original, current) || !stored.ScalarRelationSuccessor(view, first, current) || original.ScalarPublication != nil {
		t.Fatal("exact complete chain lost source ownership")
	}
	if stored.ScalarRelationSuccessor(firstView, current, first) || stored.ScalarRelationSuccessor(view, current, first) {
		t.Fatal("reverse publication chain admitted")
	}
	for _, kind := range []string{"copied-target", "copied-receipt", "missing-link", "unrelated-revision", "wrong-source", "foreign-domain", "wrong-epoch", "missing-current"} {
		t.Run(kind, func(t *testing.T) {
			checked, old, next := view, original, current
			state := catalog.CloneState(view.State())
			copy := *current
			switch kind {
			case "copied-target":
				next = &copy
				state.SetDescriptor(copy.Address(), next)
			case "copied-receipt":
				receipt := *(current.ScalarPublication.(*stored.ScalarRoutinePublication))
				copy.ScalarPublication = &receipt
				next = &copy
				state.SetDescriptor(copy.Address(), next)
			case "missing-link":
				copy.ScalarPublication = nil
				next = &copy
				state.SetDescriptor(copy.Address(), next)
			case "unrelated-revision":
				copy.ObjectRevision++
				next = &copy
				state.SetDescriptor(copy.Address(), next)
			case "wrong-source":
				before := *original
				old = &before
			case "foreign-domain":
				checked = engine.New().CatalogRead()
			case "wrong-epoch":
				checked = view.WithState(catalog.NewState(view.Epoch() + 1))
			case "missing-current":
				state.RemoveDescriptor(current.Address())
			}
			if kind != "foreign-domain" && kind != "wrong-epoch" {
				checked = view.WithState(state)
			}
			if stored.ScalarRelationSuccessor(checked, old, next) {
				t.Fatal("forged or incomplete chain admitted", kind)
			}
		})
	}
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SAVEPOINT receipt_mark")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION receipt_value() RETURNS integer RETURN 13")
	// Capture the genuine private descriptor through the incumbent journal read.
	var private *catalog.Relation
	for _, binding := range coreReceiptPrivateRelations(t, e, s) {
		if binding.Name() == "receipt_rows" {
			private = binding
		}
	}
	if private == nil {
		t.Fatal("missing private successor")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO receipt_mark")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION receipt_value() RETURNS integer RETURN 17")
	for _, binding := range coreReceiptPrivateRelations(t, e, s) {
		if binding.Name() == "receipt_rows" && binding.ScalarPublication == private.ScalarPublication {
			t.Fatal("rollback branch reused receipt")
		}
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
	if e.CatalogRead().Get(current.Address()) != current || stored.ScalarRelationSuccessor(e.CatalogRead(), private, current) {
		t.Fatal("rollback healed an abandoned branch")
	}
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SAVEPOINT receipt_release")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION receipt_value() RETURNS integer RETURN 19")
	releasedRead := coreReceiptPrivateRead(t, e, s)
	released := transformStoredRelation(t, releasedRead, "receipt_rows")
	if !stored.ScalarRelationSuccessor(releasedRead, original, released) || stored.ScalarRelationSuccessor(releasedRead, private, released) {
		t.Fatal("private publication lost exact branch lineage")
	}
	enginetest.SessionSQL(t, s, "RELEASE receipt_release")
	enginetest.SessionSQL(t, s, "COMMIT")
	if e.CatalogRead().Get(released.Address()) != released || !stored.ScalarRelationSuccessor(e.CatalogRead(), original, released) || current.Revision() >= released.Revision() || original.ScalarPublication != nil {
		t.Fatal("release/commit copied target or mutated source lineage")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE receipt_rows ADD COLUMN w integer")
	changed := e.CatalogRead()
	if stored.ScalarRelationSuccessor(changed, original, transformStoredRelation(t, changed, "receipt_rows")) {
		t.Fatal("unrelated table edit consumed scalar receipt")
	}
	enginetest.SessionSQL(t, s, "DROP TABLE receipt_rows")
	enginetest.SessionSQL(t, s, "CREATE TABLE receipt_rows(v integer DEFAULT receipt_value())")
	recreatedView := e.CatalogRead()
	recreated := transformStoredRelation(t, recreatedView, "receipt_rows")
	if recreated.Address() == original.Address() || stored.ScalarRelationSuccessor(recreatedView, original, recreated) {
		t.Fatal("recreated name healed original relation identity")
	}
}

func coreReceiptPrivateRelations(t *testing.T, e *core.Engine, s *session.Session) []*catalog.Relation {
	t.Helper()
	var result []*catalog.Relation
	for _, relation := range coreReceiptPrivateRead(t, e, s).Relations() {
		result = append(result, relation)
	}
	return result
}

func coreReceiptPrivateRead(t *testing.T, e *core.Engine, s *session.Session) catalog.Read {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return auth.Catalog
}
