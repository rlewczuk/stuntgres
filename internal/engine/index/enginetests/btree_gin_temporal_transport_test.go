package enginetests_test

import (
	"bytes"
	"context"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBtreeGINTemporalTransport(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			e, idx, root := temporalEngine(t, populated)
			view := e.CatalogRead()
			parent := view.MustRelation(idx.SQLIndex.Parent)
			store := e.Storage.Relations[parent.Address()]
			before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
			stores := map[ids.ObjectAddress]*storage.Relation{parent.Address(): store}
			roots := map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): root}
			snapshot, err := index.CaptureSnapshot(context.Background(), index.SnapshotCapture{View: view, Authority: view.State(), Relations: stores, Roots: roots, Frontier: e.Visibility.CommitSeq(), Store: func(a ids.ObjectAddress) *storage.Relation { return stores[a] }, Root: func(a ids.ObjectAddress) *storage.IndexRoot { return roots[a] }})
			if err != nil {
				t.Fatal(err)
			}
			destination := &storage.Relation{Versions: slices.Clone(store.Versions)}
			// Transport changes physical slots as well as both logical identities.
			slices.Reverse(destination.Versions)
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
			for col, tc := range temporalPrivateCases {
				query := ginInput(t, moved.Gin.Columns[col].Binding.Usage.Type, tc.boundary)
				var want []storage.GinPosting
				if populated {
					want = []storage.GinPosting{{Row: 1002, Version: 2002, Position: 2}, {Row: 1003, Version: 2003, Position: 3}}
				}
				got, err := index.GinScalarCandidates(context.Background(), moved.Gin, col, query, 3, sqltype.InputEnvironment{})
				if err != nil || !slices.Equal(got, want) {
					t.Fatal("reminted triples", tc.typ, got, want, err)
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
			targetIndex, restored := ginInstalledRoot(t, target, "temporal_private_i")
			targetStore := target.Storage.Relations[targetIndex.SQLIndex.Parent]
			ginAssertOwners(t, restored, targetStore)
			if targetIndex == idx || restored == root || restored.Gin == root.Gin || restored.Construction == root.Construction || !index.ConstructionMatches(restored, targetIndex) || len(restored.Gin.Columns) != 6 || len(restored.Entries) != len(root.Entries) {
				t.Fatal("replacement lost destination state")
			}
			for portion, columns := range [2][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
				destination := restored.Gin.Columns
				if portion == 1 {
					destination = restored.Gin.Pending
				}
				if len(columns) != len(destination) {
					t.Fatal("replacement lost split placement", portion)
				}
				for col := range columns {
					a, b := columns[col], destination[col]
					if a.Binding.Same(b.Binding) || a.Binding.Usage.Type == b.Binding.Usage.Type || len(a.Tokens) != len(b.Tokens) {
						t.Fatal("replacement retained source generation", col)
					}
					destinationType, _ := target.CatalogRead().LookupType(sqltype.ID(temporalPrivateCases[col].input))
					if b.Binding.Usage.Type != destinationType {
						t.Fatal("binding did not select actual destination type")
					}
					sourceClass, _ := view.OperatorClass(ids.OID(a.Binding.Class.OID()))
					destinationClass, _ := target.CatalogRead().OperatorClass(ids.OID(b.Binding.Class.OID()))
					sourceFamily, _ := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(a.Binding.Class.FamilyOID())})
					destinationFamily, _ := target.CatalogRead().OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(b.Binding.Class.FamilyOID())})
					pairs := [][2]catalog.Descriptor{{sourceClass, destinationClass}, {sourceFamily, destinationFamily}}
					for slot := 2; slot <= 5; slot++ {
						x := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(a.Binding.Class.Support(slot).Routine)})
						y := target.CatalogRead().MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(b.Binding.Class.Support(slot).Routine)})
						pairs = append(pairs, [2]catalog.Descriptor{x, y})
					}
					for _, pair := range pairs {
						x, y := catalog.DescriptorOrigin(pair[0]), catalog.DescriptorOrigin(pair[1])
						if x == nil || y == nil || x.Installation == y.Installation || x.Declaration != y.Declaration || x.Installation.Provider != "btree_gin" || y.Installation.Provider != "btree_gin" || x.Installation.Version != "1.3" || y.Installation.Version != "1.3" {
							t.Fatal("symbolic declaration identity or independent seal lost")
						}
					}
					for slot := 1; slot <= 5; slot++ {
						if a.Binding.Class.Support(slot).Authority == b.Binding.Class.Support(slot).Authority {
							t.Fatal("source support authority imported", col, slot)
						}
					}
					if _, err := routine.BindClassSupport(target.CatalogRead(), a.Binding.Class); err == nil {
						t.Fatal("source capture admitted by destination")
					}
					if len(a.Tokens) > 0 && &a.Tokens[0] == &b.Tokens[0] {
						t.Fatal("source dictionary backing imported")
					}
					for _, list := range b.Tokens {
						if list.Token.Kind == sqltype.GinOrdinary && list.Token.Element.Type() != b.Binding.Usage.Type {
							t.Fatal("destination token owner")
						}
					}
				}
			}
			enginetest.Run(t, target, temporalPrivateInsert(6, true))
			_, written := ginInstalledRoot(t, target, "temporal_private_i")
			ginAssertOwners(t, written, target.Storage.Relations[targetIndex.SQLIndex.Parent])
			if len(written.Entries) != len(restored.Entries)+1 || !scalarSameGin(root.Gin, before) || !scalarSameEntries(root.Entries, entries) {
				t.Fatal("destination write mutated source")
			}
			enginetest.Run(t, target, "DELETE FROM temporal_private WHERE id=6")
			var redump bytes.Buffer
			if err := engine.Dump(context.Background(), target, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatalf("replacement redump identity: %v", err)
			}
		})
	}
}
