package enginetests_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryFreezeIndexVersionsProjection(t *testing.T) {
	parent := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90000}
	idx := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}
	a := storage.RowVersion{Row: 7, Version: 11}
	b := storage.RowVersion{Row: 7, Version: 12, Base: 10}
	var registry index.ExclusionRegistry
	ticket := new(lock.Ticket)
	initial := registry.ProjectionLocked(ticket)
	edit := index.ExclusionEdit{Index: idx, Member: true, Admitted: true}
	edit.Apply(initial, parent, a)
	edit.Apply(initial, parent, b)
	if len(initial.Entries[idx]) != 2 {
		t.Fatal("second actual projection replaced sibling")
	}
	registry.InstallLocked(ticket, initial)
	captured := registry.OwnLocked(ticket)
	updated := registry.ProjectionLocked(ticket)
	edit.Apply(updated, parent, storage.RowVersion{Row: 7, Version: 13, Base: 11})
	if len(updated.Entries[idx]) != 2 || updated.Entries[idx][0].Row.Version != 12 || updated.Entries[idx][1].Row.Version != 13 {
		t.Fatal("exact base replacement removed unrelated sibling")
	}
	next := registry.ProjectionLocked(ticket)
	next.EndRow(parent, a.Row, a.Version)
	if len(next.Entries[idx]) != 1 || next.Entries[idx][0].Row.Version != b.Version || next.Ended[parent][a.Version] != a.Row {
		t.Fatal("exact end removed sibling", next)
	}
	if len(captured.Entries[idx]) != 2 || len(captured.Ended[parent]) != 0 {
		t.Fatal("published projection changed")
	}
	registry.InstallLocked(ticket, next)
	both := registry.ProjectionLocked(ticket)
	excluded := index.ExclusionEdit{Index: idx, Excluded: []storage.RowVersion{a, b}}
	excluded.Apply(both, parent, storage.RowVersion{Row: 8, Version: 13})
	if len(both.Ended[parent]) != 3 || both.Ended[parent][11] != 7 || both.Ended[parent][12] != 7 || both.Ended[parent][10] != 7 {
		t.Fatal("excluded siblings/base collapsed", both.Ended)
	}
	if len(next.Ended[parent]) != 1 {
		t.Fatal("later edit mutated captured end map")
	}
	deletes := &visibility.PrivateDeletes{}
	deletes.Set(visibility.DeleteCandidate{Row: 7, Version: 11})
	deletes.Set(visibility.DeleteCandidate{Row: 7, Version: 12})
	shape := registry.ProjectionLocked(ticket)
	descriptor := &catalog.Relation{ObjectAddress: parent}
	shape.ReplaceShape(descriptor, descriptor, nil, nil, deletes)
	if shape.Ended[parent][11] != 7 || shape.Ended[parent][12] != 7 {
		t.Fatal("shape transport collapsed actual ends")
	}
	if len(next.Ended[parent]) != 1 {
		t.Fatal("shape transport mutated predecessor")
	}
}

func TestPgsurgeryFreezeIndexVersionsDelta(t *testing.T) {
	for _, method := range []catalog.IndexMethod{catalog.BtreeMethod, catalog.HashMethod} {
		t.Run(method.String(), func(t *testing.T) {
			view := engine.New().CatalogRead()
			integer, _ := view.LookupType(sqltype.Int4)
			boolean, _ := view.LookupType(sqltype.Bool)
			value := func(n int64) sqltype.Value {
				v, err := sqltype.Integer(integer, n)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90000}, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "k", Usage: integer.Usage()}}}
			d := &catalog.IndexDefinition{Method: method, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1}}, Ready: true, Valid: true, Live: true}
			if method == catalog.BtreeMethod {
				d.MethodOID = 403
			} else {
				d.MethodOID = 405
			}
			truth := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
			if method == catalog.BtreeMethod {
				d.Predicate = &catalog.IndexPredicate{Definition: truth, Execution: truth}
			}
			idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}, SQLIndex: d}
			a := storage.RowVersion{Row: 7, Version: 11, Born: 1, Values: []sqltype.Value{value(1)}}
			b := storage.RowVersion{Row: 7, Version: 12, Born: 2, Values: []sqltype.Value{value(2)}}
			store := &storage.Relation{Versions: []storage.RowVersion{b}}
			eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			old, err := index.BuildIndexRoot(eval, parent, d, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			store.Retained = storage.RetainHeapPredecessor(a)
			store.Retained.End = 2
			store.Headers, err = storage.RepairHeapHeaders(nil, []storage.RowVersion{a})
			if err != nil {
				t.Fatal(err)
			}
			// Surgery itself does not manufacture the missing predecessor index entry.
			if method == catalog.BtreeMethod && (len(old.Entries) != 1 || old.Entries[0].Version != 12) {
				t.Fatal("repair recreated index entries")
			}
			writes := []storage.RowVersion{{Row: 7, Version: 13, Base: 11, Values: []sqltype.Value{value(3)}}, {Row: 7, Version: 14, Base: 12, Values: []sqltype.Value{value(4)}}}
			publication := &index.RowPublication{Store: store, Root: store.Versions, Writes: writes, Next: writes, Slots: []int{0, 1}}
			var merged *storage.IndexRoot
			if method == catalog.HashMethod {
				merged, err = index.MergeHashIndexRoot(eval, parent, idx, old, publication, true, true)
			} else {
				merged, err = index.MergeIndexRoot(eval, parent, idx, old.Entries, publication, true, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if method == catalog.BtreeMethod {
				if len(merged.Entries) != 2 || !merged.IsMember(7, 13) || !merged.IsMember(7, 14) {
					t.Fatal("ordinary sibling writes lost membership", merged)
				}
				merged.RowEntries = make(map[ids.RowID]int)
				for at := range merged.Entries {
					merged.SetRowEntry(at)
				}
				bound := &index.UniqueBoundKey{Index: idx}
				for _, row := range writes {
					member, keys, err := index.ExistingRootOutput(bound, merged, row, eval, index.ProbeRevalidation)
					if err != nil || !member || len(keys) != 1 || !sqltype.SameDatum(keys[0], row.Values[0]) {
						t.Fatal("probe shadowed exact sibling", row.Version, member, keys, err)
					}
				}
				if member, _, err := index.ExistingRootOutput(bound, merged, a, eval, index.ProbeRevalidation); err != nil || member {
					t.Fatal("absent predecessor gained membership", member, err)
				}
			}
			if method == catalog.HashMethod {
				merged.Hash.RowEntries = make(map[ids.RowID]*storage.HashEntry)
				for _, bucket := range merged.Hash.Buckets {
					for at := range bucket {
						merged.Hash.SetRowEntry(&bucket[at])
					}
				}
				bound := &index.UniqueBoundKey{Index: idx, Scratch: make([]sqltype.Value, 1)}
				for _, row := range writes {
					member, keys, err := index.ExistingRootOutput(bound, merged, row, eval, index.ProbeRevalidation)
					if err != nil || !member || !sqltype.SameDatum(keys[0], row.Values[0]) {
						t.Fatal("hash probe shadowed exact sibling", row.Version, member, keys, err)
					}
				}
				if member, _, err := index.ExistingRootOutput(bound, merged, a, eval, index.ProbeRevalidation); err != nil || member {
					t.Fatal("absent hash predecessor gained membership", member, err)
				}
				var count int
				for _, bucket := range old.Hash.Buckets {
					for _, entry := range bucket {
						count++
						if entry.Version != 12 {
							t.Fatal("repair recreated hash entry")
						}
					}
				}
				if count != 1 {
					t.Fatal("immutable hash predecessor changed")
				}
			}
			for _, targets := range [][]ids.VersionID{{13}, {14}, {13, 14}} {
				deletes := &visibility.PrivateDeletes{}
				for _, version := range targets {
					deletes.Set(visibility.DeleteCandidate{Row: 7, Version: version})
				}
				p := &index.RowPublication{Store: store, Root: writes, Next: writes, Deletion: deletes}
				var retired *storage.IndexRoot
				if method == catalog.HashMethod {
					retired, err = index.MergeHashIndexRoot(eval, parent, idx, merged, p, false, true)
				} else {
					retired, err = index.MergeIndexRoot(eval, parent, idx, merged.Entries, p, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				var versions []ids.VersionID
				if method == catalog.HashMethod {
					for _, bucket := range retired.Hash.Buckets {
						for _, entry := range bucket {
							versions = append(versions, entry.Version)
						}
					}
				} else {
					for _, entry := range retired.Entries {
						versions = append(versions, entry.Version)
					}
				}
				if len(versions) != 2-len(targets) {
					t.Fatal("sibling end count collapsed", targets, versions)
				}
				for _, version := range versions {
					for _, target := range targets {
						if version == target {
							t.Fatal("target index member survived", target)
						}
					}
				}
			}
			if method == catalog.BtreeMethod && (len(old.Entries) != 1 || old.Entries[0].Version != 12 || len(merged.Entries) != 2 || !merged.IsMember(7, 13) || !merged.IsMember(7, 14)) {
				t.Fatal("delta mutated immutable predecessor")
			}
		})
	}
}

func TestPgsurgeryFreezeIndexVersionsGIN(t *testing.T) {
	f := newGinFixture(t)
	entries := []storage.IndexEntry{{Row: 7, Version: 11, Position: 0, Keys: f.root.Entries[0].Keys}, {Row: 7, Version: 12, Position: 1, Keys: f.root.Entries[1].Keys}, {Row: 8, Version: 13, Position: 2, Keys: f.root.Entries[0].Keys}}
	store := &storage.Relation{}
	for _, entry := range entries {
		store.Versions = append(store.Versions, storage.RowVersion{Row: entry.Row, Version: entry.Version, Born: 1, Values: entry.Keys})
	}
	// A genuine partial GIN emits exact membership maps, including the two
	// versions of Row 7. GIN emits no unique/exclusion entry-ordinal map.
	definition := *f.descriptor.SQLIndex
	boolean, _ := f.view.LookupType(sqltype.Bool)
	truth := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	definition.Predicate = &catalog.IndexPredicate{Definition: truth, Execution: truth}
	descriptor := *f.descriptor
	descriptor.SQLIndex = &definition
	built, err := index.BuildIndexRoot(f.evaluation, f.parent, &definition, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []sqltype.GinBinding{built.Gin.Columns[0].Binding, built.Gin.Columns[1].Binding}
	p11 := storage.GinPosting{Row: 7, Version: 11, Position: 0}
	p12 := storage.GinPosting{Row: 7, Version: 12, Position: 1}
	p13 := storage.GinPosting{Row: 8, Version: 13, Position: 2}
	// Enumerate the two dictionaries independently of Extract/Build/Merge:
	// V11/V13 have array keys 1,2; V12 has 2,3; all have lexemes cat,rat.
	expectedColumns := func(nonNull, one, two, three []storage.GinPosting) []storage.GinColumn {
		columns := []storage.GinColumn{{Binding: bindings[0], NonNull: nonNull}, {Binding: bindings[1], NonNull: nonNull}}
		element := f.parent.Columns[0].Usage.Type.BaseType().Element()
		for i, postings := range [][]storage.GinPosting{one, two, three} {
			if len(postings) != 0 {
				value, err := sqltype.Integer(element, int64(i+1))
				if err != nil {
					t.Fatal(err)
				}
				columns[0].Tokens = append(columns[0].Tokens, storage.GinPostingList{Token: sqltype.GinToken{Kind: sqltype.GinOrdinary, Element: value}, Postings: postings})
			}
		}
		if len(nonNull) != 0 {
			columns[1].Tokens = []storage.GinPostingList{
				{Token: sqltype.GinToken{Kind: sqltype.GinOrdinary, Lexeme: "cat"}, Postings: nonNull},
				{Token: sqltype.GinToken{Kind: sqltype.GinOrdinary, Lexeme: "rat"}, Postings: nonNull},
			}
		}
		return columns
	}
	all := expectedColumns([]storage.GinPosting{p11, p12, p13}, []storage.GinPosting{p11, p13}, []storage.GinPosting{p11, p12, p13}, []storage.GinPosting{p12})
	without11 := expectedColumns([]storage.GinPosting{p12, p13}, []storage.GinPosting{p13}, []storage.GinPosting{p12, p13}, []storage.GinPosting{p12})
	without12 := expectedColumns([]storage.GinPosting{p11, p13}, []storage.GinPosting{p11, p13}, []storage.GinPosting{p11, p13}, nil)
	only11 := expectedColumns([]storage.GinPosting{p11}, []storage.GinPosting{p11}, []storage.GinPosting{p11}, nil)
	only13 := expectedColumns([]storage.GinPosting{p13}, []storage.GinPosting{p13}, []storage.GinPosting{p13}, nil)
	empty := expectedColumns(nil, nil, nil, nil)
	checkColumns := func(t *testing.T, got, want []storage.GinColumn) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatal("GIN portion arity", len(got), len(want))
		}
		for ordinal, expected := range want {
			actual := got[ordinal]
			if !actual.Binding.Same(expected.Binding) || !slices.Equal(actual.NonNull, expected.NonNull) || len(actual.Tokens) != len(expected.Tokens) {
				t.Fatal("complete GIN dictionary/NonNull survivors", ordinal, actual, expected)
			}
			for at, list := range expected.Tokens {
				if !reflect.DeepEqual(actual.Tokens[at].Token, list.Token) || !slices.Equal(actual.Tokens[at].Postings, list.Postings) {
					t.Fatal("complete GIN token/full-triple survivors", ordinal, at, actual.Tokens[at], list)
				}
			}
		}
	}
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "main-and-pending"}[split], func(t *testing.T) {
			source := *built
			if split {
				source.Gin, err = storage.BuildGin(context.Background(), bindings, entries[:1], sqltype.InputEnvironment{})
				if err == nil {
					source.Gin, err = storage.MergeGin(context.Background(), source.Gin, nil, entries[1:], nil, sqltype.InputEnvironment{})
				}
				if err != nil || storage.GinPendingIncidences(source.Gin) == 0 {
					t.Fatal("missing genuine pending siblings", err)
				}
			}
			before := source
			before.Gin = ginCopyPayload(source.Gin)
			before.Entries = gistCopyEntries(&source)
			before.Membership = maps.Clone(source.Membership)
			before.MembershipVersions = maps.Clone(source.MembershipVersions)
			for _, tc := range []struct {
				name                string
				targets             map[ids.VersionID]ids.RowID
				survivors           []int
				bulk, main, pending []storage.GinColumn
				changed             bool
			}{
				{"end-11", map[ids.VersionID]ids.RowID{11: 7}, []int{1, 2}, without11, empty, without11, true},
				{"end-12", map[ids.VersionID]ids.RowID{12: 7}, []int{0, 2}, without12, only11, only13, true},
				{"end-both", map[ids.VersionID]ids.RowID{11: 7, 12: 7}, []int{2}, only13, empty, only13, true},
				{"wrong-associated-rows", map[ids.VersionID]ids.RowID{11: 8, 12: 8}, []int{0, 1, 2}, all, only11, without11, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					checkPayload := func(payload *storage.GinRoot) {
						t.Helper()
						main, pending := tc.bulk, []storage.GinColumn(nil)
						if split {
							main, pending = tc.main, tc.pending
						}
						checkColumns(t, payload.Columns, main)
						checkColumns(t, payload.Pending, pending)
					}
					if err := storage.ValidateGinMerge(context.Background(), source.Gin, nil, nil, tc.targets, sqltype.InputEnvironment{}); err != nil {
						t.Fatal(err)
					}
					next, err := storage.MergeGin(context.Background(), source.Gin, nil, nil, tc.targets, sqltype.InputEnvironment{})
					if err != nil {
						t.Fatal(err)
					}
					checkPayload(next)
					deletes := &visibility.PrivateDeletes{}
					for version, row := range tc.targets {
						deletes.Set(visibility.DeleteCandidate{Row: row, Version: version})
					}
					publication := &index.RowPublication{Store: store, Root: store.Versions, Deletion: deletes}
					if candidate, err := index.MergeGinIndexRoot(f.evaluation, f.parent, &descriptor, &source, publication, false, false); err != nil || candidate != nil {
						t.Fatal("nonmaterializing owning retirement", candidate, err)
					}
					merged, err := index.MergeGinIndexRoot(f.evaluation, f.parent, &descriptor, &source, publication, false, true)
					if err != nil || (merged != nil) != tc.changed {
						t.Fatal("owning retirement/no-change decision", merged, err)
					}
					if merged == nil {
						merged = &source
					}
					checkPayload(merged.Gin)
					var wantEntries []storage.IndexEntry
					wantMembers := make(map[ids.VersionID]ids.RowID)
					for _, at := range tc.survivors {
						wantEntries = append(wantEntries, entries[at])
						wantMembers[entries[at].Version] = entries[at].Row
					}
					if !reflect.DeepEqual(merged.Entries, wantEntries) || merged.Construction != source.Construction || merged.Membership == nil || merged.MembershipLen() != len(wantEntries) {
						t.Fatal("owning exact Entries/slots/construction/membership", merged, wantEntries)
					}
					actualMembers := make(map[ids.VersionID]ids.RowID)
					for row, version := range merged.Members() {
						if _, duplicate := actualMembers[version]; duplicate {
							t.Fatal("duplicate exact membership metadata", row, version)
						}
						actualMembers[version] = row
					}
					if !maps.Equal(actualMembers, wantMembers) || merged.IsMember(8, 11) || merged.IsMember(8, 12) || merged.RowEntries != nil || merged.RowEntryVersions != nil || merged.RowEntryLen() != 0 {
						t.Fatal("owning survivor metadata or unexpected GIN ordinal map", actualMembers, wantMembers)
					}
					for _, entry := range entries {
						if merged.IsMember(entry.Row, entry.Version) != (wantMembers[entry.Version] == entry.Row) {
							t.Fatal("owning exact sibling membership", entry)
						}
					}
					for _, materialize := range []bool{false, true} {
						canceled := *f.evaluation
						canceled.Context = &spgistInterruptContext{Context: context.Background(), remaining: 1}
						if out, err := index.MergeGinIndexRoot(&canceled, f.parent, &descriptor, &source, publication, false, materialize); out != nil || !errors.Is(err, context.Canceled) {
							t.Fatal("canceled sibling retirement escaped", out, err)
						}
					}
					if !reflect.DeepEqual(&source, &before) {
						t.Fatal("retirement mutated immutable source payload or metadata")
					}
				})
			}
			for _, materialize := range []bool{false, true} {
				invalid := &index.RowPublication{Store: store, Root: store.Versions, SingleSlot: -1, Writes: []storage.RowVersion{{
					Row: 7, Version: 14, Base: 11, Values: entries[0].Keys,
					IndexOutputs: map[ids.ObjectAddress]storage.IndexOutput{descriptor.Address(): {Index: &descriptor, Member: true, Keys: entries[0].Keys[:1], GinPending: split}},
				}}}
				if out, err := index.MergeGinIndexRoot(f.evaluation, f.parent, &descriptor, &source, invalid, false, materialize); out != nil || err == nil {
					t.Fatal("malformed sibling output became installable", out, err)
				}
			}
			if !reflect.DeepEqual(&source, &before) {
				t.Fatal("failed owning merge mutated immutable source")
			}
		})
	}
}
