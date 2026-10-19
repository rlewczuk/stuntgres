package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestDomainCheckIndexPrivateLayoutTransport(t *testing.T) {
	for _, definition := range []struct{ name, index string }{
		{"btree_predicate", "CREATE INDEX checked_idx ON subject(id) WHERE (id::checked)::integer > 0"},
		{"btree_key", "CREATE INDEX checked_idx ON subject(((id::checked)::integer))"},
		{"hash_key", "CREATE INDEX checked_idx ON subject USING hash(((id::checked)::integer))"},
	} {
		t.Run(definition.name, func(t *testing.T) {
			e := New()
			for _, query := range []string{"CREATE SEQUENCE steps", "CREATE DOMAIN checked AS integer CHECK(nextval('steps') > 0)", "CREATE TABLE subject(id integer, unused integer)", definition.index, "INSERT INTO subject VALUES(1,9),(2,9)"} {
				run(t, e, query)
			}
			sequence := roleTableRef(t, e, "steps").Address
			if e.storage.sequences[sequence].last != 2 {
				t.Fatal("initial index insertion events", e.storage.sequences[sequence].last)
			}
			session := sessionStart(t, e, nil)
			sessionSQL(t, session, "BEGIN")
			sessionSQL(t, session, "UPDATE subject SET id=3 WHERE id=1")
			if e.storage.sequences[sequence].last != 3 {
				t.Fatal("UPDATE replayed predecessor or omitted successor", e.storage.sequences[sequence].last)
			}
			sessionSQL(t, session, "ALTER TABLE subject DROP COLUMN unused")
			if e.storage.sequences[sequence].last != 3 {
				t.Fatal("slot projection replayed CHECK", e.storage.sequences[sequence].last)
			}
			sessionSQL(t, session, "COMMIT")
			if e.storage.sequences[sequence].last != 3 {
				t.Fatal("publication replayed projected CHECK", e.storage.sequences[sequence].last)
			}
			parent := roleTableRef(t, e, "subject").Address
			root := e.storage.indexRoots[roleTableRef(t, e, "checked_idx").Address]
			store := e.storage.relations[parent]
			check := func(row ids.RowID, version ids.VersionID, position int) {
				if position < 0 || position >= len(store.versions) || store.versions[position].row != row || store.versions[position].version != version {
					t.Fatal("projected entry lost actual heap identity/position")
				}
			}
			if root.hash != nil {
				count := 0
				for _, bucket := range root.hash.buckets {
					for _, entry := range bucket {
						count++
						check(entry.row, entry.version, entry.position)
					}
				}
				if count != 2 || root.hash.storage != store {
					t.Fatal("projected hash root coverage/storage")
				}
			} else {
				if len(root.entries) != 2 {
					t.Fatal("projected btree coverage", len(root.entries))
				}
				for _, entry := range root.entries {
					check(entry.row, entry.version, entry.position)
				}
			}
		})
	}
}

func TestDomainCheckIndexAutocommitReindexEvents(t *testing.T) {
	e := New()
	for _, query := range []string{"CREATE SEQUENCE steps", "CREATE DOMAIN checked AS integer CHECK(nextval('steps') > 0)", "CREATE TABLE subject(id integer)", "CREATE INDEX checked_idx ON subject(((id::checked)::integer))", "INSERT INTO subject VALUES(1),(2)"} {
		run(t, e, query)
	}
	sequence := roleTableRef(t, e, "steps").Address
	if e.storage.sequences[sequence].last != 2 {
		t.Fatal("initial insertion events")
	}
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "REINDEX INDEX checked_idx")
	if e.storage.sequences[sequence].last != 4 {
		t.Fatal("first real REINDEX build", e.storage.sequences[sequence].last)
	}
	sessionSQL(t, session, "REINDEX INDEX checked_idx")
	if e.storage.sequences[sequence].last != 6 {
		t.Fatal("earlier build suppressed new REINDEX", e.storage.sequences[sequence].last)
	}
}
