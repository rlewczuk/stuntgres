package indexes_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestDomainCheckIndexSnapshotTransport(t *testing.T) {
	for _, definition := range []struct{ name, table, index string }{
		{"btree_predicate", "CREATE TABLE subject(id integer)", "CREATE INDEX checked_idx ON subject(id) WHERE (id::checked)::integer > 0"},
		{"btree_key", "CREATE TABLE subject(id integer)", "CREATE INDEX checked_idx ON subject(((id::checked)::integer))"},
		{"hash_key", "CREATE TABLE subject(id integer)", "CREATE INDEX checked_idx ON subject USING hash(((id::checked)::integer))"},
		{"btree_domain_datum", "CREATE TABLE subject(id checked)", "CREATE INDEX checked_idx ON subject(id)"},
		{"hash_domain_datum", "CREATE TABLE subject(id checked)", "CREATE INDEX checked_idx ON subject USING hash(id)"},
	} {
		t.Run(definition.name, func(t *testing.T) {
			for _, self := range []bool{false, true} {
				t.Run(map[bool]string{false: "foreign", true: "self"}[self], func(t *testing.T) {
					source := engine.New()
					for _, query := range []string{"CREATE SEQUENCE steps", "CREATE DOMAIN checked AS integer CHECK (nextval('steps') > 0)", definition.table, definition.index, "INSERT INTO subject VALUES (1),(2)", "DELETE FROM subject WHERE id=1"} {
						enginetest.Run(t, source, query)
					}
					sequence := enginetest.RoleTableRef(t, source, "steps").Address
					if source.Storage.Sequences[sequence].Last != 2 {
						t.Fatal("index event replayed before snapshot", source.Storage.Sequences[sequence].Last)
					}
					indexAddress := enginetest.RoleTableRef(t, source, "checked_idx").Address
					parentAddress := enginetest.RoleTableRef(t, source, "subject").Address
					oldRoot, oldStore := source.Storage.IndexRoots[indexAddress], source.Storage.Relations[parentAddress]
					oldRows := slices.Clone(oldStore.Versions)
					target := engine.New()
					if self {
						target = source
					}
					if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
						t.Fatal(err)
					}
					importedSequence := enginetest.RoleTableRef(t, target, "steps").Address
					if target.Storage.Sequences[importedSequence].Last != 2 || source.Storage.Sequences[sequence].Last != 2 {
						t.Fatal("snapshot replayed CHECK SQL")
					}
					newStore := target.Storage.Relations[enginetest.RoleTableRef(t, target, "subject").Address]
					newRoot := target.Storage.IndexRoots[enginetest.RoleTableRef(t, target, "checked_idx").Address]
					if len(newStore.Versions) != 1 || newRoot == oldRoot || newStore == oldStore {
						t.Fatal("snapshot retained old physical authority")
					}
					parent := target.Catalog.State.MustRelation(enginetest.RoleTableRef(t, target, "subject").Address)
					if newRoot.Hash != nil {
						count := 0
						for _, bucket := range newRoot.Hash.Buckets {
							for _, entry := range bucket {
								count++
								if entry.Position != 0 || entry.Row != newStore.Versions[0].Row || entry.Version != newStore.Versions[0].Version {
									t.Fatal("hash entry retained source identities or positions")
								}
								if entry.Key.Type() != parent.Columns[0].Usage.Type {
									t.Fatal("hash datum retained foreign type generation")
								}
							}
						}
						if count != 1 || newRoot.Hash.Storage != newStore {
							t.Fatal("hash snapshot coverage or storage authority")
						}
					} else if len(newRoot.Entries) != 1 || newRoot.Entries[0].Position != 0 || newRoot.Entries[0].Row != newStore.Versions[0].Row || newRoot.Entries[0].Version != newStore.Versions[0].Version {
						t.Fatal("btree snapshot retained source identities or positions")
					}
					if newRoot.Hash == nil && newRoot.Entries[0].Keys[0].Type() != parent.Columns[0].Usage.Type {
						t.Fatal("btree datum retained foreign type generation")
					}
					for i, row := range oldRows {
						if row.Row != oldStore.Versions[i].Row || row.Version != oldStore.Versions[i].Version {
							t.Fatal("snapshot mutated source headers")
						}
					}
				})
			}
		})
	}
}
