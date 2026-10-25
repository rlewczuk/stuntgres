package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestSequencePreparedNamespaceCallback(t *testing.T) {
	for _, event := range []string{"Create", "Rename", "ACL"} {
		t.Run(event, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SCHEMA unrelated", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
				enginetest.SessionSQL(t, s, q)
			}
			ast, description := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
			held := func(want int64) {
				t.Helper()
				r, err := sequencePreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				got := enginetest.Tuples(r)
				r.Rows.Close()
				if len(got) != 1 || got[0][0] != want {
					t.Fatal("held sequence identity", got, want)
				}
			}
			held(10)
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE a.s START 100 CACHE 1")
			held(11) // Relation creation alone does not invalidate the old capture.
			switch event {
			case "Create":
				enginetest.SessionSQL(t, s, "CREATE SCHEMA callback_created")
			case "Rename":
				enginetest.SessionSQL(t, s, "ALTER SCHEMA unrelated RENAME TO callback_renamed")
			case "ACL":
				if err := enginetest.SetObjectACL(e, context.Background(), schemaRef(t, e, "unrelated"), authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
			}
			held(100) // Every event leaves the effective a,b path unchanged.
			sequenceScalar(t, s, "SELECT last_value FROM b.s", 11)
			sequenceScalar(t, s, "SELECT last_value FROM a.s", 100)
			sequenceScalar(t, s, "SELECT nextval('s')", 101) // Direct source resolves a.s too.
			sequenceScalar(t, s, "SELECT nextval('b.s')", 12)
		})
	}
}

func schemaRef(t *testing.T, e *engine.Engine, name string) ids.ObjectRef {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if a == (ids.ObjectAddress{}) {
		t.Fatal("missing namespace", name)
	}
	return e.Catalog.Domain.Ref(v.Epoch(), a)
}
