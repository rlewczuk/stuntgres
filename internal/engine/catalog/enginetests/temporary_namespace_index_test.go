package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// TestTemporaryNamespaceIndex checks that the temporary-namespace map holds
// exactly the session-owned namespaces a full descriptor walk finds, as
// sessions create temporary objects and end (whatever session end retains).
func TestTemporaryNamespaceIndex(t *testing.T) {
	e := engine.New()
	// check compares the index with a full walk and returns the count.
	check := func() int {
		t.Helper()
		read := e.CatalogRead()
		walked := map[ids.ObjectAddress]*catalog.Namespace{}
		for a, d := range read.Descriptors() {
			if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary != (ids.SessionToken{}) {
				walked[a] = ns
			}
		}
		indexed := map[ids.ObjectAddress]*catalog.Namespace{}
		for a, ns := range read.TemporaryNamespaces() {
			indexed[a] = ns
		}
		if len(indexed) != len(walked) {
			t.Fatalf("temporary namespaces: walked %d, indexed %d", len(walked), len(indexed))
		}
		for a, ns := range walked {
			if indexed[a] != ns {
				t.Fatal("index entry differs from descriptor", a)
			}
		}
		return len(walked)
	}
	end := func(s *session.Session) {
		t.Helper()
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if check() != 0 {
		t.Fatal("fresh engine has temporary namespaces")
	}
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TEMP TABLE a_tmp(v INT)")
	first := check()
	enginetest.SessionSQL(t, b, "CREATE TEMP TABLE b_tmp(v INT)")
	enginetest.SessionSQL(t, b, "CREATE TEMP SEQUENCE b_seq")
	both := check()
	if first == 0 || both <= first {
		t.Fatal("sessions created no temporary namespaces", first, both)
	}
	end(a)
	check()
	end(b)
	check()
}
