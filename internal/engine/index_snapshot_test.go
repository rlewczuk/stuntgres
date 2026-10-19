package engine

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
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
					source := New()
					for _, query := range []string{"CREATE SEQUENCE steps", "CREATE DOMAIN checked AS integer CHECK (nextval('steps') > 0)", definition.table, definition.index, "INSERT INTO subject VALUES (1),(2)", "DELETE FROM subject WHERE id=1"} {
						run(t, source, query)
					}
					sequence := roleTableRef(t, source, "steps").Address
					if source.storage.sequences[sequence].last != 2 {
						t.Fatal("index event replayed before snapshot", source.storage.sequences[sequence].last)
					}
					indexAddress := roleTableRef(t, source, "checked_idx").Address
					parentAddress := roleTableRef(t, source, "subject").Address
					oldRoot, oldStore := source.storage.indexRoots[indexAddress], source.storage.relations[parentAddress]
					oldRows := slices.Clone(oldStore.versions)
					target := New()
					if self {
						target = source
					}
					if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
						t.Fatal(err)
					}
					importedSequence := roleTableRef(t, target, "steps").Address
					if target.storage.sequences[importedSequence].last != 2 || source.storage.sequences[sequence].last != 2 {
						t.Fatal("snapshot replayed CHECK SQL")
					}
					newStore := target.storage.relations[roleTableRef(t, target, "subject").Address]
					newRoot := target.storage.indexRoots[roleTableRef(t, target, "checked_idx").Address]
					if len(newStore.versions) != 1 || newRoot == oldRoot || newStore == oldStore {
						t.Fatal("snapshot retained old physical authority")
					}
					parent := target.catalog.state.MustRelation(roleTableRef(t, target, "subject").Address)
					if newRoot.hash != nil {
						count := 0
						for _, bucket := range newRoot.hash.buckets {
							for _, entry := range bucket {
								count++
								if entry.position != 0 || entry.row != newStore.versions[0].row || entry.version != newStore.versions[0].version {
									t.Fatal("hash entry retained source identities or positions")
								}
								if entry.key.Type() != parent.Columns[0].Usage.Type {
									t.Fatal("hash datum retained foreign type generation")
								}
							}
						}
						if count != 1 || newRoot.hash.storage != newStore {
							t.Fatal("hash snapshot coverage or storage authority")
						}
					} else if len(newRoot.entries) != 1 || newRoot.entries[0].position != 0 || newRoot.entries[0].row != newStore.versions[0].row || newRoot.entries[0].version != newStore.versions[0].version {
						t.Fatal("btree snapshot retained source identities or positions")
					}
					if newRoot.hash == nil && newRoot.entries[0].keys[0].Type() != parent.Columns[0].Usage.Type {
						t.Fatal("btree datum retained foreign type generation")
					}
					for i, row := range oldRows {
						if row.row != oldStore.versions[i].row || row.version != oldStore.versions[i].version {
							t.Fatal("snapshot mutated source headers")
						}
					}
				})
			}
		})
	}
}

func TestDomainCheckIndexSnapshotRejectsMissingAndMalformedRoots(t *testing.T) {
	f := newPartialIndexFixture(t)
	ctx := context.Background()
	if _, err := f.e.captureIndexSnapshot(ctx, f.e.CatalogRead(), f.e.storage.relations, nil, f.e.commitSeq); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("missing root accepted", err)
	}
	original := f.e.storage.indexRoots[f.index.Address()]
	malformed := *original
	malformed.entries = slices.Clone(original.entries)
	malformed.entries[0].position = len(f.store.versions)
	roots := map[ids.ObjectAddress]*logicalIndexRoot{f.index.Address(): &malformed}
	previous := f.e.storage.indexRoots
	f.e.storage.indexRoots = roots
	if _, err := f.e.captureIndexSnapshot(ctx, f.e.CatalogRead(), f.e.storage.relations, roots, f.e.commitSeq); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("malformed source provenance accepted", err)
	}
	f.e.storage.indexRoots = previous
	if original.entries[0].position == malformed.entries[0].position {
		t.Fatal("rejection mutated source")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.e.captureIndexSnapshot(canceled, f.e.CatalogRead(), f.e.storage.relations, f.e.storage.indexRoots, f.e.commitSeq); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

// Exercise the scalar transport seam, not an excluded ACL index provider.
func TestDomainCheckIndexSnapshotACLReownership(t *testing.T) {
	source, target := New(), New()
	aclUnitRoles(source, "snapshot_reader", "snapshot_grantor")
	aclUnitRoles(target, "snapshot_spacer", "snapshot_reader", "snapshot_grantor")
	value := aclUnitValue(t, source, "snapshot_reader=r*/snapshot_grantor")
	fromArray := aclUnitType(t, source, sqltype.ACLItemArray)
	toArray := aclUnitType(t, target, sqltype.ACLItemArray)
	array, err := sqltype.OwnedArrayValue(fromArray, []sqltype.ArrayDimension{{Lower: -1, Length: 2}}, []sqltype.Value{value, sqltype.Null(value.Type())})
	if err != nil {
		t.Fatal(err)
	}
	fromDomain, err := sqltype.Domain(45000, "snapshot_acl", "snapshot_acl", fromArray.Usage())
	if err != nil {
		t.Fatal(err)
	}
	toDomain, err := sqltype.Domain(45000, "snapshot_acl", "snapshot_acl", toArray.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := fromDomain.Wrap(array)
	if err != nil {
		t.Fatal(err)
	}
	fromComposite, err := sqltype.Composite(45001, "snapshot_record", "snapshot_record", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "nested", Usage: fromDomain.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	toComposite, err := sqltype.Composite(45001, "snapshot_record", "snapshot_record", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "nested", Usage: toDomain.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	composite, err := sqltype.OwnedCompositeValue(fromComposite, []sqltype.Value{wrapped})
	if err != nil {
		t.Fatal(err)
	}
	importer := target.aclItemImporter(source, source.Roles(), target.Roles(), nil)
	snapshot := &indexSnapshot{}
	mapped, err := snapshot.datum(composite, toComposite, nil, importer)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := mapped.CompositeField(0).BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	authority, grantee, grantor, privileges, options, err := nested.ArrayElement(0).ACLItem()
	reader, _ := target.Roles().Lookup("snapshot_reader")
	writer, _ := target.Roles().Lookup("snapshot_grantor")
	if err != nil || authority != &target.aclAuthority || grantee != uint32(reader.Ref.ID()) || grantor != uint32(writer.Ref.ID()) || privileges != 2 || options != 2 {
		t.Fatal("snapshot lost ACL principal authority/options", err)
	}
	if mapped.Type() != toComposite || mapped.CompositeField(0).Type() != toDomain || nested.Type() != toArray || !nested.ArrayElement(1).IsNull() {
		t.Fatal("snapshot lost recursive target generations or NULL")
	}
	if array.ArrayElement(0) != value || composite.CompositeField(0).Type() != fromDomain {
		t.Fatal("snapshot mutated source payload")
	}
	again, err := snapshot.datum(mapped, toComposite, nil, importer)
	if err != nil || again != mapped {
		t.Fatal("destination-owned ACL copied/reimported", err)
	}
	if source.catalog.state.ACLItemImport() != nil || target.catalog.state.ACLItemImport() != nil {
		t.Fatal("snapshot published import callback")
	}
}
