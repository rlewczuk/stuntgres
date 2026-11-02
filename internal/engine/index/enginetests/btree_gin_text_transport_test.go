package enginetests_test

import (
	"bytes"
	"context"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBtreeGINTextTransport(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			e, idx, root := textEngine(t, populated)
			view := e.CatalogRead()
			parent := view.MustRelation(idx.SQLIndex.Parent)
			store := e.Storage.Relations[parent.Address()]
			before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			baseline := &storage.Relation{}
			positions := make(map[index.RowIdentity]int)
			if populated {
				for slot, position := range []int{0, 1} {
					row := store.Versions[position]
					positions[index.RowIdentity{Row: row.Row, Version: row.Version}] = slot
					baseline.Versions = append(baseline.Versions, row)
				}
			}
			build := &index.AlterIndexBuild{Index: idx, Root: root, Store: store}
			filtered, err := build.BaselineRoot(evaluation, idx, baseline, positions)
			if err != nil {
				t.Fatal(err)
			}
			ginAssertOwners(t, filtered, baseline)
			if len(filtered.Entries) != len(baseline.Versions) || filtered.Construction != root.Construction {
				t.Fatal("baseline lost exact construction/membership")
			}
			for col, tc := range textPrivateCases {
				column := filtered.Gin.Columns[col]
				var want []storage.GinPosting
				if populated {
					row := baseline.Versions[1]
					want = []storage.GinPosting{{Row: row.Row, Version: row.Version, Position: 1}}
				}
				got, err := index.GinScalarCandidates(context.Background(), column, ginInput(t, column.Binding.Usage.Type, tc.boundary), 3, sqltype.InputEnvironment{})
				if err != nil || !slices.Equal(got, want) {
					t.Fatal("baseline destination triples", col, got, want, err)
				}
			}
			targetParent := *parent
			integer, _ := view.LookupType(sqltype.Int4)
			targetParent.Attributes = append([]int32{99}, parent.Attributes...)
			targetParent.Columns = append([]catalog.Column{{Name: "padding", Usage: integer.Usage()}}, parent.Columns...)
			targetStore := &storage.Relation{Versions: slices.Clone(store.Versions)}
			for i := range targetStore.Versions {
				targetStore.Versions[i].Values = append([]sqltype.Value{sqltype.Null(integer)}, store.Versions[i].Values...)
			}
			layoutIndex := *idx
			journal := &session.Journal{}
			_, _, err = index.TransportCapturedIndexLayouts(evaluation, journal, []index.LayoutCapture{{Lineage: index.LayoutLineage{Source: idx, Target: &layoutIndex, SourceParent: parent, TargetParent: &targetParent}, Root: root}}, store, targetStore, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			layout := index.JournalIndexesOrZero(journal).UniqueRoots[idx.Address()]
			if layout == nil || layout.Gin != root.Gin || layout.Construction == root.Construction || !index.ConstructionMatches(layout, &layoutIndex) {
				t.Fatal("layout rebuilt payload or lost construction lineage")
			}
			stores := map[ids.ObjectAddress]*storage.Relation{parent.Address(): store}
			roots := map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): root}
			snapshot, err := index.CaptureSnapshot(context.Background(), index.SnapshotCapture{View: view, Authority: view.State(), Relations: stores, Roots: roots, Frontier: e.Visibility.CommitSeq(), Store: func(a ids.ObjectAddress) *storage.Relation { return stores[a] }, Root: func(a ids.ObjectAddress) *storage.IndexRoot { return roots[a] }})
			if err != nil {
				t.Fatal(err)
			}
			destination := &storage.Relation{Versions: slices.Clone(store.Versions)}
			physical := make(map[index.RowIdentity]int)
			for i := range destination.Versions {
				row := &destination.Versions[i]
				physical[index.RowIdentity{Row: row.Row, Version: row.Version}] = i
				row.Row = ids.RowID(1000 + i)
				row.Version = ids.VersionID(2000 + i)
			}
			transported, err := snapshot.Transport(context.Background(), view, map[ids.ObjectAddress]*storage.Relation{parent.Address(): destination}, map[ids.ObjectAddress]map[index.RowIdentity]int{parent.Address(): physical})
			if err != nil {
				t.Fatal(err)
			}
			moved := transported[idx.Address()]
			ginAssertOwners(t, moved, destination)
			if moved == root || moved.Gin == root.Gin || moved.Construction == root.Construction || !index.ConstructionMatches(moved, idx) {
				t.Fatal("transport imported construction owner")
			}
			for col, tc := range textPrivateCases {
				column := moved.Gin.Columns[col]
				var want []storage.GinPosting
				if populated {
					want = []storage.GinPosting{{Row: 1001, Version: 2001, Position: 1}, {Row: 1002, Version: 2002, Position: 2}}
				}
				got, err := index.GinScalarCandidates(context.Background(), column, ginInput(t, column.Binding.Usage.Type, tc.boundary), 3, sqltype.InputEnvironment{})
				if err != nil || !slices.Equal(got, want) {
					t.Fatal("exact reminted triples", tc.typ, got, want, err)
				}
			}
			var dump bytes.Buffer
			if err := engine.Dump(context.Background(), e, &dump); err != nil {
				t.Fatal(err)
			}
			target := engine.New()
			if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			targetIndex, restored := ginInstalledRoot(t, target, "text_private_i")
			ginAssertOwners(t, restored, target.Storage.Relations[targetIndex.SQLIndex.Parent])
			if targetIndex == idx || restored == root || restored.Gin == root.Gin || len(restored.Entries) != len(root.Entries) {
				t.Fatal("replacement lost destination state")
			}
			for col, tc := range textPrivateCases {
				a, b := root.Gin.Columns[col], restored.Gin.Columns[col]
				destinationType, _ := target.CatalogRead().LookupType(sqltype.ID(tc.input))
				if a.Binding.Same(b.Binding) || a.Binding.Usage.Type == b.Binding.Usage.Type || b.Binding.Usage.Type != destinationType || len(a.Tokens) != len(b.Tokens) {
					t.Fatal("replacement retained source generation")
				}
				sourceClass, _ := view.OperatorClass(ids.OID(a.Binding.Class.OID()))
				destClass, _ := target.CatalogRead().OperatorClass(ids.OID(b.Binding.Class.OID()))
				sourceFamily, _ := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(a.Binding.Class.FamilyOID())})
				destFamily, _ := target.CatalogRead().OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(b.Binding.Class.FamilyOID())})
				pairs := [][2]catalog.Descriptor{{sourceClass, destClass}, {sourceFamily, destFamily}}
				for slot := 2; slot <= 5; slot++ {
					x := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(a.Binding.Class.Support(slot).Routine)})
					y := target.CatalogRead().MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(b.Binding.Class.Support(slot).Routine)})
					pairs = append(pairs, [2]catalog.Descriptor{x, y})
				}
				for _, pair := range pairs {
					x, y := catalog.DescriptorOrigin(pair[0]), catalog.DescriptorOrigin(pair[1])
					if x == nil || y == nil || x.Installation == y.Installation || x.Declaration != y.Declaration || x.Installation.Provider != "btree_gin" || y.Installation.Provider != "btree_gin" || x.Installation.Version != "1.3" || y.Installation.Version != "1.3" {
						t.Fatal("symbolic declaration or independent provider seal lost")
					}
				}
				for slot := 1; slot <= 5; slot++ {
					if a.Binding.Class.Support(slot).Authority == b.Binding.Class.Support(slot).Authority {
						t.Fatal("source authority imported")
					}
				}
				if _, err := routine.BindClassSupport(target.CatalogRead(), a.Binding.Class); err == nil {
					t.Fatal("destination admitted detached source capture")
				}
				if populated && &a.Tokens[0] == &b.Tokens[0] {
					t.Fatal("source dictionary backing imported")
				}
				for _, list := range b.Tokens {
					if list.Token.Kind == sqltype.GinOrdinary && list.Token.Element.Type() != destinationType {
						t.Fatal("destination token owner")
					}
				}
			}
			enginetest.Run(t, target, textPrivateInsert(6, true))
			_, written := ginInstalledRoot(t, target, "text_private_i")
			ginAssertOwners(t, written, target.Storage.Relations[targetIndex.SQLIndex.Parent])
			if len(written.Entries) != len(restored.Entries)+1 || !scalarSameGin(root.Gin, before) || !scalarSameEntries(root.Entries, entries) {
				t.Fatal("destination write mutated source")
			}
			enginetest.Run(t, target, "DELETE FROM text_private WHERE id=6")
			var redump bytes.Buffer
			if err := engine.Dump(context.Background(), target, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatalf("replacement redump differs: %v", err)
			}
		})
	}
}
