package engine

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPartialIndexUniqueTypeFinalMembershipBaseline(t *testing.T) {
	for _, overlay := range []string{"none", "delete", "outside_successor", "member_successor"} {
		t.Run(overlay, func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			store := uniquePublicationStore(f.stores[f.parent.address])
			original := uniquePublicationStore(&store)
			// This is the effective final TYPE row image: the transformed predicate
			// column admits a formerly excluded duplicate. The real retained predicate
			// is evaluated against final Usage, not a mock expression or key validator.
			store.versions[2].values[3] = store.versions[0].values[3]
			var writes []rowVersion
			var deletes *privateDeletionRoot
			if overlay == "delete" {
				r := store.versions[2]
				deletes = &privateDeletionRoot{rows: map[RowID]deleteCandidate{r.row: {ref: f.ref, storage: &store, row: r.row, version: r.version}}}
			}
			if overlay == "outside_successor" || overlay == "member_successor" {
				active := "false"
				if overlay == "member_successor" {
					active = "true"
				}
				r := partialUniqueRow(t, f, 1, "2001-02-03", "1", active)
				r.row, r.base = store.versions[2].row, store.versions[2].version
				writes = []rowVersion{r}
			}
			before := uniquePublicationStore(&store)
			roots, err := validateAlterUniques(context.Background(), f.auth, f.auth.catalog, f.parent, &store, writes, deletes, "public", sqltype.OutputEnvironment{}, []columnTransformation{{attribute: f.parent.attributes[3], rewrite: true}}, nil, nil)
			if overlay == "none" || overlay == "member_successor" {
				if roots != nil {
					t.Fatal("failed TYPE returned baseline")
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui_index"`, Detail: "Key (a, b)=(2001-02-03, 1) is duplicated.", Schema: "public", Table: "ui_t", Constraint: "ui_index"})
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(store, before) || reflect.DeepEqual(store, original) {
				t.Fatal("final TYPE candidate mutated or not actually transformed")
			}
			f.unchanged(t)
		})
	}
}

func TestPartialIndexUniqueTypePrivateRootOwnership(t *testing.T) {
	f := newPartialUniquePublication(t, true, false, "table")
	baseline := uniquePublicationStore(f.stores[f.parent.address])
	baseline.versions = slices.Clone(baseline.versions[:2])
	// Real candidate descriptor/predicate, separately owned private publication.
	next := *f.index
	definition := *f.index.sqlIndex
	predicate := *definition.predicate
	definition.predicate = &predicate
	next.sqlIndex = &definition
	changes := &catalogChanges{descriptors: map[ObjectAddress]Descriptor{next.address: &next}, originals: map[ObjectAddress]Descriptor{next.address: f.index}}
	if err := prepareAlterUniqueRoots(context.Background(), changes, f.auth.catalog, f.parent, &baseline, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	root := changes.uniqueRoots[next.address]
	if root == nil || root == f.roots[next.address] {
		t.Fatal("private baseline borrowed committed root")
	}
	want := map[RowID]VersionID{baseline.versions[0].row: baseline.versions[0].version, baseline.versions[1].row: baseline.versions[1].version}
	if !maps.Equal(root.membership, want) || len(root.entries) != 2 {
		t.Fatal("private TYPE membership", root)
	}
	cloned := cloneCatalogChanges(changes)
	if cloned.uniqueRoots[next.address] != root {
		t.Fatal("savepoint clone lost immutable baseline")
	}
	delete(cloned.uniqueRoots, next.address)
	if changes.uniqueRoots[next.address] != root {
		t.Fatal("savepoint map aliases private owner")
	}
	validated := map[string]*logicalIndexRoot{next.name: root}
	other := &catalogChanges{descriptors: changes.descriptors, originals: changes.originals}
	if err := prepareAlterUniqueRoots(context.Background(), other, f.auth.catalog, f.parent, &baseline, validated, false, nil); err != nil {
		t.Fatal(err)
	}
	if other.uniqueRoots[next.address] != root {
		t.Fatal("validated immutable baseline not reused")
	}
	f.unchanged(t)
}
