package engine

import (
	"context"
	"testing"
)

func TestSequencePreparedNamespaceCallback(t *testing.T) {
	for _, event := range []string{"Create", "Rename", "ACL"} {
		t.Run(event, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SCHEMA unrelated", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
				sessionSQL(t, s, q)
			}
			ast, description := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
			held := func(want int64) {
				t.Helper()
				r, err := sequencePreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				got := tuples(r)
				r.Rows.Close()
				if len(got) != 1 || got[0][0] != want {
					t.Fatal("held sequence identity", got, want)
				}
			}
			held(10)
			sessionSQL(t, s, "CREATE SEQUENCE a.s START 100 CACHE 1")
			held(11) // Relation creation alone does not invalidate the old capture.
			switch event {
			case "Create":
				sessionSQL(t, s, "CREATE SCHEMA callback_created")
			case "Rename":
				sessionSQL(t, s, "ALTER SCHEMA unrelated RENAME TO callback_renamed")
			case "ACL":
				if err := e.SetObjectACL(context.Background(), schemaRef(t, e, "unrelated"), ACLValue{Explicit: true}); err != nil {
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
