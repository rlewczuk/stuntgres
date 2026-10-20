package engine

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPartialIndexUniqueTypeFinalMembershipBaseline(t *testing.T) {
	for _, overlay := range []string{"none", "delete", "outside_successor", "member_successor"} {
		t.Run(overlay, func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			store := uniquePublicationStore(f.stores[f.parent.Address()])
			original := uniquePublicationStore(&store)
			// This is the effective final TYPE row image: the transformed predicate
			// column admits a formerly excluded duplicate. The real retained predicate
			// is evaluated against final Usage, not a mock expression or key validator.
			store.Versions[2].Values[3] = store.Versions[0].Values[3]
			var writes []storage.RowVersion
			var deletes *visibility.PrivateDeletes
			if overlay == "delete" {
				r := store.Versions[2]
				deletes = &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{r.Row: {Ref: f.ref, Storage: &store, Row: r.Row, Version: r.Version}}}
			}
			if overlay == "outside_successor" || overlay == "member_successor" {
				active := "false"
				if overlay == "member_successor" {
					active = "true"
				}
				r := partialUniqueRow(t, f, 1, "2001-02-03", "1", active)
				r.Row, r.Base = store.Versions[2].Row, store.Versions[2].Version
				writes = []storage.RowVersion{r}
			}
			before := uniquePublicationStore(&store)
			roots, err := validateAlterIndexBuilds(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: f.auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.auth, f.auth.Catalog, f.parent, &store, writes, deletes, "public", sqltype.OutputEnvironment{}, []columnTransformation{{attribute: f.parent.Attributes[3], rewrite: true}}, nil, nil, true)
			if overlay == "none" || overlay == "member_successor" {
				if roots != nil {
					t.Fatal("failed TYPE returned baseline")
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui_index"`, Detail: "Key (a, b)=(2001-02-03, 1) is duplicated.", Schema: "public", Table: "ui_t", Constraint: "ui_index"})
			} else if err != nil {
				t.Fatal(err)
			}
			if err == nil {
				build := roots[f.index.Name()]
				if build == nil || build.root == nil || build.store == &store {
					t.Fatal("successful overlay lost its produced root or compact layout", build)
				}
				for _, entry := range build.root.Entries {
					if entry.Row == store.Versions[2].Row {
						t.Fatal("owner-invisible predecessor/excluded successor became an index member", entry)
					}
				}
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
	baseline := uniquePublicationStore(f.stores[f.parent.Address()])
	baseline.Versions = slices.Clone(baseline.Versions[:2])
	// Real candidate descriptor/predicate, separately owned private publication.
	next := *f.index
	definition := *f.index.SQLIndex
	predicate := *definition.Predicate
	definition.Predicate = &predicate
	next.SQLIndex = &definition
	changes := &session.Journal{Descriptors: map[ids.ObjectAddress]catalog.Descriptor{next.Address(): &next}, Originals: map[ids.ObjectAddress]catalog.Descriptor{next.Address(): f.index}}
	if err := prepareAlterIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: f.auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, changes, f.auth.Catalog, f.parent, &baseline, nil, false); err != nil {
		t.Fatal(err)
	}
	root := journalIndexesOrZero(changes).uniqueRoots[next.Address()]
	if root == nil || root == f.roots[next.Address()] {
		t.Fatal("private baseline borrowed committed root")
	}
	want := map[ids.RowID]ids.VersionID{baseline.Versions[0].Row: baseline.Versions[0].Version, baseline.Versions[1].Row: baseline.Versions[1].Version}
	if !maps.Equal(root.Membership, want) || len(root.Entries) != 2 {
		t.Fatal("private TYPE membership", root)
	}
	cloned := changes.Clone()
	if journalIndexesOrZero(cloned).uniqueRoots[next.Address()] != root {
		t.Fatal("savepoint clone lost immutable baseline")
	}
	delete(journalIndexesOrZero(cloned).uniqueRoots, next.Address())
	if journalIndexesOrZero(changes).uniqueRoots[next.Address()] != root {
		t.Fatal("savepoint map aliases private owner")
	}
	validated := map[string]*alterIndexBuild{next.Name(): {index: &next, root: root, store: &baseline}}
	other := &session.Journal{Descriptors: changes.Descriptors, Originals: changes.Originals}
	if err := prepareAlterIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: f.auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, other, f.auth.Catalog, f.parent, &baseline, validated, false); err != nil {
		t.Fatal(err)
	}
	if journalIndexesOrZero(other).uniqueRoots[next.Address()] != root {
		t.Fatal("validated immutable baseline not reused")
	}
	f.unchanged(t)
}
