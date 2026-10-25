package indexcmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestDomainCheckIndexPrivateLayoutTransport(t *testing.T) {
	for _, definition := range []struct{ name, index string }{
		{"btree_predicate", "CREATE INDEX checked_idx ON subject(id) WHERE (id::checked)::integer > 0"},
		{"btree_key", "CREATE INDEX checked_idx ON subject(((id::checked)::integer))"},
		{"hash_key", "CREATE INDEX checked_idx ON subject USING hash(((id::checked)::integer))"},
	} {
		t.Run(definition.name, func(t *testing.T) {
			e := engine.New()
			for _, query := range []string{"CREATE SEQUENCE steps", "CREATE DOMAIN checked AS integer CHECK(nextval('steps') > 0)", "CREATE TABLE subject(id integer, unused integer)", definition.index, "INSERT INTO subject VALUES(1,9),(2,9)"} {
				enginetest.Run(t, e, query)
			}
			sequence := enginetest.RoleTableRef(t, e, "steps").Address
			if e.Storage.Sequences[sequence].Last != 2 {
				t.Fatal("initial index insertion events", e.Storage.Sequences[sequence].Last)
			}
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "BEGIN")
			enginetest.SessionSQL(t, session, "UPDATE subject SET id=3 WHERE id=1")
			if e.Storage.Sequences[sequence].Last != 3 {
				t.Fatal("UPDATE replayed predecessor or omitted successor", e.Storage.Sequences[sequence].Last)
			}
			enginetest.SessionSQL(t, session, "ALTER TABLE subject DROP COLUMN unused")
			if e.Storage.Sequences[sequence].Last != 3 {
				t.Fatal("slot projection replayed CHECK", e.Storage.Sequences[sequence].Last)
			}
			enginetest.SessionSQL(t, session, "COMMIT")
			if e.Storage.Sequences[sequence].Last != 3 {
				t.Fatal("publication replayed projected CHECK", e.Storage.Sequences[sequence].Last)
			}
			parent := enginetest.RoleTableRef(t, e, "subject").Address
			root := e.Storage.IndexRoots[enginetest.RoleTableRef(t, e, "checked_idx").Address]
			store := e.Storage.Relations[parent]
			check := func(row ids.RowID, version ids.VersionID, position int) {
				if position < 0 || position >= len(store.Versions) || store.Versions[position].Row != row || store.Versions[position].Version != version {
					t.Fatal("projected entry lost actual heap identity/position")
				}
			}
			if root.Hash != nil {
				count := 0
				for _, bucket := range root.Hash.Buckets {
					for _, entry := range bucket {
						count++
						check(entry.Row, entry.Version, entry.Position)
					}
				}
				if count != 2 || root.Hash.Storage != store {
					t.Fatal("projected hash root coverage/storage")
				}
			} else {
				if len(root.Entries) != 2 {
					t.Fatal("projected btree coverage", len(root.Entries))
				}
				for _, entry := range root.Entries {
					check(entry.Row, entry.Version, entry.Position)
				}
			}
		})
	}
}

func TestDomainCheckIndexAutocommitReindexEvents(t *testing.T) {
	e := engine.New()
	for _, query := range []string{"CREATE SEQUENCE steps", "CREATE DOMAIN checked AS integer CHECK(nextval('steps') > 0)", "CREATE TABLE subject(id integer)", "CREATE INDEX checked_idx ON subject(((id::checked)::integer))", "INSERT INTO subject VALUES(1),(2)"} {
		enginetest.Run(t, e, query)
	}
	sequence := enginetest.RoleTableRef(t, e, "steps").Address
	if e.Storage.Sequences[sequence].Last != 2 {
		t.Fatal("initial insertion events")
	}
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "REINDEX INDEX checked_idx")
	if e.Storage.Sequences[sequence].Last != 4 {
		t.Fatal("first real REINDEX build", e.Storage.Sequences[sequence].Last)
	}
	enginetest.SessionSQL(t, session, "REINDEX INDEX checked_idx")
	if e.Storage.Sequences[sequence].Last != 6 {
		t.Fatal("earlier build suppressed new REINDEX", e.Storage.Sequences[sequence].Last)
	}
}
