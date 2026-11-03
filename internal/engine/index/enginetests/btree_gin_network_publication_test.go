package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func networkBuild(t *testing.T, view catalog.Read, parent, idx *catalog.Relation, store *storage.Relation) *storage.IndexRoot {
	t.Helper()
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(eval, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func TestBtreeGINNetworkPublication(t *testing.T) {
	t.Run("produced_delta", func(t *testing.T) {
		e, idx, parent, store, root := networkPhysical(t)
		before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
		eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		row := storage.RowVersion{Row: 199, Version: 299, Values: slices.Clone(store.Versions[3].Values), IndexOutputs: map[ids.ObjectAddress]storage.IndexOutput{idx.Address(): {Index: idx, Member: true, Keys: root.Entries[3].Keys}}}
		delta := &index.RowPublication{Store: store, Root: store.Versions, SingleSlot: -1, Writes: []storage.RowVersion{row}}
		candidate, err := index.MergeGinIndexRoot(eval, parent, idx, root, delta, true, false)
		if err != nil || candidate != nil {
			t.Fatal("admission constructed installable root", candidate, err)
		}
		candidate, err = index.MergeGinIndexRoot(eval, parent, idx, root, delta, true, true)
		if err != nil || candidate == nil {
			t.Fatal("missing materialized control", err)
		}
		for col, tc := range networkPrivateCases {
			query := ginInput(t, candidate.Gin.Columns[col].Binding.Usage.Type, tc.upper)
			got, err := index.GinScalarCandidates(context.Background(), candidate.Gin, col, query, 3, sqltype.InputEnvironment{})
			want := []storage.GinPosting{{Row: 104, Version: 204, Position: 3}, {Row: 199, Version: 299, Position: 5}}
			if err != nil || !slices.Equal(got, want) {
				t.Fatal("produced delta physical owner", tc.typ, got, want, err)
			}
			boundary := sqltype.GinToken{Element: ginInput(t, root.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}
			a, b := ginLookup(t, root.Gin.Columns[col], boundary), ginLookup(t, candidate.Gin.Columns[col], boundary)
			if &a[0] != &b[0] {
				t.Fatal("unaffected posting backing copied", tc.typ)
			}
		}
		for _, materialize := range []bool{false, true} {
			bad := row
			bad.IndexOutputs = map[ids.ObjectAddress]storage.IndexOutput{idx.Address(): {Index: idx, Member: true, Keys: []sqltype.Value{sqltype.Null(parent.Columns[0].Usage.Type)}}}
			invalid := *delta
			invalid.Writes = []storage.RowVersion{bad}
			if out, err := index.MergeGinIndexRoot(eval, parent, idx, root, &invalid, true, materialize); err == nil || out != nil {
				t.Fatal("nonmaterializing admission omitted fallible produced-key validation", out, err)
			}
			interrupted := *eval
			interrupted.Context = &spgistInterruptContext{Context: context.Background(), remaining: 3}
			out, err := index.MergeGinIndexRoot(&interrupted, parent, idx, root, delta, true, materialize)
			if !errors.Is(err, context.Canceled) || out != nil {
				t.Fatal("canceled merge exposed partial candidate", materialize, out, err)
			}
		}
		// Repeated source memberships amplify survivor output, not delta scratch.
		// This is a real valid dictionary/root, produced through the ordinary builder.
		var small int64
		for _, count := range []int{32, 2048} {
			expanded := &storage.Relation{}
			for i := range count {
				v := store.Versions[i%4]
				v.Row = ids.RowID(1000 + i)
				v.Version = ids.VersionID(2000 + i)
				v.Values = slices.Clone(v.Values)
				v.Values[6] = ginInput(t, parent.Columns[6].Usage.Type, strconv.Itoa(i))
				expanded.Versions = append(expanded.Versions, v)
			}
			large := networkBuild(t, e.CatalogRead(), parent, idx, expanded)
			add := row
			add.Row = 9000
			add.Version = 9000
			batch := &index.RowPublication{Store: expanded, Root: expanded.Versions, SingleSlot: -1, Writes: []storage.RowVersion{add}}
			var out *storage.IndexRoot
			var failure error
			_, bytes := util.AllocationsPerCall(3, func() { out, failure = index.MergeGinIndexRoot(eval, parent, idx, large, batch, true, false) })
			if failure != nil || out != nil {
				t.Fatal("admission control", failure, out)
			}
			if count == 32 {
				small = bytes
			} else if bytes > small+8192 {
				t.Fatalf("admission allocated survivor-sized output: small=%d large=%d", small, bytes)
			}
		}
		if !reflect.DeepEqual(root.Gin, before) || !reflect.DeepEqual(root.Entries, entries) {
			t.Fatal("delta changed predecessor value graph")
		}
	})
	t.Run("live_end_update_rollback", func(t *testing.T) {
		e, idx, old := networkEngine(t, true)
		before, entries := ginCopyPayload(old.Gin), gistCopyEntries(old)
		enginetest.Run(t, e, networkPrivateInsert(6, false))
		_, inserted := ginInstalledRoot(t, e, "network_private_i")
		ginAssertOwners(t, inserted, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range networkPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}
			a, b := ginLookup(t, old.Gin.Pending[col], token), ginLookup(t, inserted.Gin.Pending[col], token)
			if &a[0] != &b[0] {
				t.Fatal("append rebuilt unaffected posting", tc.typ)
			}
		}
		enginetest.Run(t, e, "UPDATE network_private SET inet='0.0.0.0/0',cidr='0.0.0.0/0',macaddr='00:00:00:00:00:00',macaddr8='00:00:00:00:00:00:00:00',uuid='00000000-0000-0000-0000-000000000000',oid=0 WHERE id=2")
		_, updated := ginInstalledRoot(t, e, "network_private_i")
		ginAssertOwners(t, updated, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range networkPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}
			previous := ginLookup(t, old.Gin.Pending[col], token)
			got := ginLookup(t, updated.Gin.Pending[col], token)
			if len(previous) != 2 || len(got) != 1 || got[0] != previous[1] {
				t.Fatal("update ended wrong predecessor", tc.typ, got, previous)
			}
		}
		enginetest.Run(t, e, "DELETE FROM network_private WHERE id=3")
		_, deleted := ginInstalledRoot(t, e, "network_private_i")
		ginAssertOwners(t, deleted, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range networkPrivateCases {
			if got := ginLookup(t, deleted.Gin.Pending[col], sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}); len(got) != 0 {
				t.Fatal("ended duplicate remains", tc.typ, got)
			}
		}
		session := enginetest.StartSession(t, e, nil)
		tx := enginetest.TransactionBegin(t, session)
		enginetest.SessionSQL(t, session, networkPrivateInsert(7, true))
		if e.Storage.IndexRoots[idx.Address()] != deleted {
			t.Fatal("private posting escaped")
		}
		enginetest.TransactionEnd(t, session, tx, false)
		if e.Storage.IndexRoots[idx.Address()] != deleted {
			t.Fatal("rollback changed predecessor")
		}
		tx = enginetest.TransactionBegin(t, session)
		enginetest.SessionSQL(t, session, networkPrivateInsert(7, true))
		enginetest.SessionSQL(t, session, "SAVEPOINT network_saved")
		enginetest.SessionSQL(t, session, networkPrivateInsert(8, true))
		enginetest.SessionSQL(t, session, "ROLLBACK TO network_saved")
		enginetest.TransactionEnd(t, session, tx, true)
		_, committed := ginInstalledRoot(t, e, "network_private_i")
		ginAssertOwners(t, committed, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range networkPrivateCases {
			if got := ginLookup(t, committed.Gin.Pending[col], sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}); len(got) != 1 {
				t.Fatal("savepoint exact survivor", tc.typ, got)
			}
		}
		enginetest.Run(t, e, "ALTER TABLE network_private ADD CONSTRAINT network_positive CHECK(id>0)")
		_, admitted := ginInstalledRoot(t, e, "network_private_i")
		saved := ginCopyPayload(admitted.Gin)
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "INSERT INTO network_private(id,inet) VALUES(90,'10.0.0.0/24'),(-1,'10.0.0.0/24')"), nil)
		if err == nil || e.Storage.IndexRoots[idx.Address()] != admitted || !reflect.DeepEqual(admitted.Gin, saved) {
			t.Fatal("partial failed statement publication", err)
		}
		if !reflect.DeepEqual(old.Gin, before) || !reflect.DeepEqual(old.Entries, entries) {
			t.Fatal("live publication mutated original")
		}
	})
}
