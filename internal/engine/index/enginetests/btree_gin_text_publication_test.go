package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBtreeGINTextPublication(t *testing.T) {
	t.Run("produced_delta", func(t *testing.T) {
		e, idx, parent, store, root := textPhysical(t)
		before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
		eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		row := storage.RowVersion{Row: 199, Version: 299, Values: slices.Clone(store.Versions[0].Values), IndexOutputs: map[ids.ObjectAddress]storage.IndexOutput{idx.Address(): {Index: idx, Member: true, Keys: root.Entries[0].Keys}}}
		delta := &index.RowPublication{Store: store, Root: store.Versions, SingleSlot: -1, Writes: []storage.RowVersion{row}}
		if out, err := index.MergeGinIndexRoot(eval, parent, idx, root, delta, true, false); err != nil || out != nil {
			t.Fatal("nonmaterializing admission", out, err)
		}
		next, err := index.MergeGinIndexRoot(eval, parent, idx, root, delta, true, true)
		if err != nil {
			t.Fatal(err)
		}
		for col, tc := range textPrivateCases {
			column := next.Gin.Columns[col]
			got, err := index.GinScalarCandidates(context.Background(), column, ginInput(t, column.Binding.Usage.Type, tc.lower), 3, sqltype.InputEnvironment{})
			want := append(compoundExactPostings([]int{0}), storage.GinPosting{Row: 199, Version: 299, Position: 8})
			if err != nil || !slices.Equal(got, want) {
				t.Fatal("produced exact triples", tc.typ, got, want, err)
			}
			token := sqltype.GinToken{Element: ginInput(t, column.Binding.Usage.Type, tc.upper)}
			a, b := ginLookup(t, root.Gin.Columns[col], token), ginLookup(t, column, token)
			if &a[0] != &b[0] {
				t.Fatal("unaffected posting backing rebuilt", tc.typ)
			}
		}
		for _, materialize := range []bool{false, true} {
			invalid := *delta
			bad := row
			bad.IndexOutputs = map[ids.ObjectAddress]storage.IndexOutput{idx.Address(): {Index: idx, Member: true, Keys: []sqltype.Value{sqltype.Null(parent.Columns[0].Usage.Type)}}}
			invalid.Writes = []storage.RowVersion{row, bad}
			if out, err := index.MergeGinIndexRoot(eval, parent, idx, root, &invalid, true, materialize); err == nil || out != nil {
				t.Fatal("late invalid candidate published", out, err)
			}
			canceled := *eval
			canceled.Context = &spgistInterruptContext{Context: context.Background(), remaining: 3}
			if out, err := index.MergeGinIndexRoot(&canceled, parent, idx, root, delta, true, materialize); !errors.Is(err, context.Canceled) || out != nil {
				t.Fatal("canceled partial candidate", out, err)
			}
		}
		if !scalarSameGin(root.Gin, before) || !scalarSameEntries(root.Entries, entries) {
			t.Fatal("delta mutated predecessor")
		}
	})
	t.Run("live_publication", func(t *testing.T) {
		e, idx, old := textEngine(t, true)
		before, entries := ginCopyPayload(old.Gin), gistCopyEntries(old)
		enginetest.Run(t, e, textPrivateInsert(6, false))
		_, inserted := ginInstalledRoot(t, e, "text_private_i")
		ginAssertOwners(t, inserted, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range textPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.upper)}
			a, b := ginLookup(t, old.Gin.Columns[col], token), ginLookup(t, inserted.Gin.Columns[col], token)
			if &a[0] != &b[0] {
				t.Fatal("append rebuilt unaffected list")
			}
		}
		enginetest.Run(t, e, `UPDATE text_private SET c0='b',c1='b',c2='b',c3='\377',c4='b',c5='\xff' WHERE id=2`)
		_, updated := ginInstalledRoot(t, e, "text_private_i")
		ginAssertOwners(t, updated, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range textPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}
			prior := ginLookup(t, old.Gin.Columns[col], token)
			got := ginLookup(t, updated.Gin.Columns[col], token)
			if !slices.Equal(got, prior[1:]) {
				t.Fatal("update did not end exact predecessor", tc.typ, got, prior)
			}
		}
		enginetest.Run(t, e, "DELETE FROM text_private WHERE id=3")
		_, deleted := ginInstalledRoot(t, e, "text_private_i")
		ginAssertOwners(t, deleted, e.Storage.Relations[idx.SQLIndex.Parent])
		session := enginetest.StartSession(t, e, nil)
		tx := enginetest.TransactionBegin(t, session)
		enginetest.SessionSQL(t, session, textPrivateInsert(7, true))
		if e.Storage.IndexRoots[idx.Address()] != deleted {
			t.Fatal("private root escaped")
		}
		enginetest.TransactionEnd(t, session, tx, false)
		if e.Storage.IndexRoots[idx.Address()] != deleted {
			t.Fatal("rollback changed root")
		}
		tx = enginetest.TransactionBegin(t, session)
		enginetest.SessionSQL(t, session, textPrivateInsert(7, true))
		enginetest.SessionSQL(t, session, "SAVEPOINT text_saved")
		enginetest.SessionSQL(t, session, textPrivateInsert(8, true))
		enginetest.SessionSQL(t, session, "ROLLBACK TO text_saved")
		enginetest.TransactionEnd(t, session, tx, true)
		_, committed := ginInstalledRoot(t, e, "text_private_i")
		ginAssertOwners(t, committed, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range textPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}
			if got := ginLookup(t, committed.Gin.Columns[col], token); len(got) != 1 {
				t.Fatal("savepoint survivors", got)
			}
		}
		enginetest.Run(t, e, "ALTER TABLE text_private ADD CONSTRAINT text_positive CHECK(id>0)")
		_, admitted := ginInstalledRoot(t, e, "text_private_i")
		saved := ginCopyPayload(admitted.Gin)
		if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "INSERT INTO text_private(id,c0) VALUES(90,'ab'),(-1,'ab')"), nil); err == nil || e.Storage.IndexRoots[idx.Address()] != admitted || !scalarSameGin(admitted.Gin, saved) {
			t.Fatal("failed statement leaked root", err)
		}
		if !scalarSameGin(old.Gin, before) || !scalarSameEntries(old.Entries, entries) {
			t.Fatal("publication mutated retained source")
		}
	})
}
