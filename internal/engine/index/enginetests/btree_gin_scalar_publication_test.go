package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func scalarPrivateInsert(id int, boundary bool) string {
	values := []string{fmt.Sprint(id)}
	for _, tc := range scalarPrivateCases {
		text := tc.lower
		if boundary {
			text = tc.boundary
		}
		values = append(values, "'"+text+"'")
	}
	return "INSERT INTO scalar_private VALUES(" + strings.Join(values, ",") + ")"
}
func TestBtreeGINScalarPublication(t *testing.T) {
	t.Run("produced_delta", func(t *testing.T) {
		e, idx, parent, store, root := scalarPhysical(t)
		before, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
		eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		row := storage.RowVersion{Row: 199, Version: 299, Values: slices.Clone(store.Versions[0].Values), IndexOutputs: map[ids.ObjectAddress]storage.IndexOutput{idx.Address(): {Index: idx, Member: true, Keys: root.Entries[0].Keys}}}
		delta := &index.RowPublication{Store: store, Root: store.Versions, SingleSlot: -1, Writes: []storage.RowVersion{row}}
		if candidate, err := index.MergeGinIndexRoot(eval, parent, idx, root, delta, true, false); err != nil || candidate != nil {
			t.Fatal("admission constructed root", candidate, err)
		}
		candidate, err := index.MergeGinIndexRoot(eval, parent, idx, root, delta, true, true)
		if err != nil {
			t.Fatal(err)
		}
		for col, tc := range scalarPrivateCases {
			old := root.Gin.Columns[col]
			next := candidate.Gin.Columns[col]
			query := ginInput(t, next.Binding.Usage.Type, tc.lower)
			want := compoundExactPostings([]int{0})
			if tc.typ == "bool" {
				want = compoundExactPostings([]int{0, 1})
			}
			want = append(want, storage.GinPosting{Row: 199, Version: 299, Position: 17})
			got, err := index.GinScalarCandidates(context.Background(), candidate.Gin, col, query, 3, sqltype.InputEnvironment{})
			if err != nil || !slices.Equal(got, want) {
				t.Fatal("published exact produced triples", tc.typ, got, want, err)
			}
			token := sqltype.GinToken{Element: ginInput(t, old.Binding.Usage.Type, tc.upper)}
			a, b := ginLookup(t, old, token), ginLookup(t, next, token)
			if &a[0] != &b[0] {
				t.Fatal("unaffected list rebuilt", tc.typ)
			}
		}
		for _, materialize := range []bool{false, true} {
			invalid := *delta
			bad := row
			bad.IndexOutputs = map[ids.ObjectAddress]storage.IndexOutput{idx.Address(): {Index: idx, Member: true, Keys: []sqltype.Value{sqltype.Null(parent.Columns[0].Usage.Type)}}}
			invalid.Writes = []storage.RowVersion{bad}
			if out, err := index.MergeGinIndexRoot(eval, parent, idx, root, &invalid, true, materialize); err == nil || out != nil {
				t.Fatal("admission skipped validation", out, err)
			}
			canceled := *eval
			canceled.Context = &spgistInterruptContext{Context: context.Background(), remaining: 3}
			if out, err := index.MergeGinIndexRoot(&canceled, parent, idx, root, delta, true, materialize); !errors.Is(err, context.Canceled) || out != nil {
				t.Fatal("partial canceled merge", out, err)
			}
		}
		if !scalarSameGin(root.Gin, before) || !scalarSameEntries(root.Entries, entries) {
			t.Fatal("delta mutated predecessor")
		}
	})
	t.Run("live_publication", func(t *testing.T) {
		e, idx, old := scalarEngine(t, true)
		before, entries := ginCopyPayload(old.Gin), gistCopyEntries(old)
		enginetest.Run(t, e, scalarPrivateInsert(6, false))
		_, inserted := ginInstalledRoot(t, e, "scalar_private_i")
		ginAssertOwners(t, inserted, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range scalarPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.upper)}
			a, b := ginLookup(t, old.Gin.Pending[col], token), ginLookup(t, inserted.Gin.Pending[col], token)
			if &a[0] != &b[0] {
				t.Fatal("append rebuilt unaffected upper", tc.typ)
			}
		}
		enginetest.Run(t, e, "UPDATE scalar_private SET int2=32767,int4=2147483647,int8=9223372036854775807,float4='NaN',float8='NaN',bool=true WHERE id=2")
		_, updated := ginInstalledRoot(t, e, "scalar_private_i")
		ginAssertOwners(t, updated, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range scalarPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}
			previous := ginLookup(t, old.Gin.Pending[col], token)
			want := []storage.GinPosting{previous[len(previous)-1]}
			if tc.typ == "bool" {
				want = []storage.GinPosting{previous[0], previous[2], ginLookup(t, inserted.Gin.Pending[col], token)[3]}
			}
			got := ginLookup(t, updated.Gin.Pending[col], token)
			if !slices.Equal(got, want) {
				t.Fatal("update ended exact predecessor", tc.typ, got, want)
			}
		}
		enginetest.Run(t, e, "DELETE FROM scalar_private WHERE id=3")
		_, deleted := ginInstalledRoot(t, e, "scalar_private_i")
		ginAssertOwners(t, deleted, e.Storage.Relations[idx.SQLIndex.Parent])
		session := enginetest.StartSession(t, e, nil)
		tx := enginetest.TransactionBegin(t, session)
		enginetest.SessionSQL(t, session, scalarPrivateInsert(7, true))
		if e.Storage.IndexRoots[idx.Address()] != deleted {
			t.Fatal("private root escaped")
		}
		enginetest.TransactionEnd(t, session, tx, false)
		if e.Storage.IndexRoots[idx.Address()] != deleted {
			t.Fatal("rollback changed root")
		}
		tx = enginetest.TransactionBegin(t, session)
		enginetest.SessionSQL(t, session, scalarPrivateInsert(7, true))
		enginetest.SessionSQL(t, session, "SAVEPOINT scalar_saved")
		enginetest.SessionSQL(t, session, scalarPrivateInsert(8, true))
		enginetest.SessionSQL(t, session, "ROLLBACK TO scalar_saved")
		enginetest.TransactionEnd(t, session, tx, true)
		_, committed := ginInstalledRoot(t, e, "scalar_private_i")
		ginAssertOwners(t, committed, e.Storage.Relations[idx.SQLIndex.Parent])
		for col, tc := range scalarPrivateCases {
			token := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[col].Binding.Usage.Type, tc.boundary)}
			got := ginLookup(t, committed.Gin.Pending[col], token)
			want := 1
			if tc.typ == "bool" {
				want = 3
			}
			if len(got) != want {
				t.Fatal("savepoint survivor", tc.typ, got)
			}
		}
		enginetest.Run(t, e, "ALTER TABLE scalar_private ADD CONSTRAINT scalar_positive CHECK(id>0)")
		_, admitted := ginInstalledRoot(t, e, "scalar_private_i")
		saved := ginCopyPayload(admitted.Gin)
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "INSERT INTO scalar_private(id,int8) VALUES(90,9007199254740993),(-1,9007199254740993)"), nil)
		if err == nil || e.Storage.IndexRoots[idx.Address()] != admitted || !scalarSameGin(admitted.Gin, saved) {
			t.Fatal("failed statement leaked publication", err)
		}
		if !scalarSameGin(old.Gin, before) || !scalarSameEntries(old.Entries, entries) {
			t.Fatal("publication mutated original")
		}
	})
}
