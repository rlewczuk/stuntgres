package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func insertConflictWantHints(t *testing.T, root *logicalIndexRoot, store *relationStorage, want map[RowID]int) {
	t.Helper()
	if root == nil || len(root.entries) != len(want) {
		t.Fatal("wrong hint membership", root, want)
	}
	for _, entry := range root.entries {
		position, ok := want[entry.row]
		if !ok || entry.position != position {
			t.Fatal("wrong actual physical slot", entry, want)
		}
		row, err := conflictEntryVersion(store.versions, entry)
		if err != nil || row.row != entry.row || row.version != entry.version {
			t.Fatal("invalid immutable root/storage pair", entry, err)
		}
		if len(row.values) != 0 && &row.values[0] != &store.versions[position].values[0] {
			t.Fatal("version recovery copied values")
		}
		if root.membership != nil && root.membership[entry.row] != entry.version {
			t.Fatal("partial membership lost identity", entry)
		}
	}
}

func TestInsertConflictIndexPositionHints(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonpartial", true: "partial"}[partial], func(t *testing.T) {
			f := insertConflictUniqueFixture(t)
			definition := *f.index.sqlIndex
			if !partial {
				definition.predicate = nil
			}
			f.index.sqlIndex = &definition
			for i, k := range []int64{20, 30} {
				values := insertConflictUnitRow(t, f, k)
				if i == 0 {
					values[1] = sqltype.Boolean(f.parent.columns[1].Usage.Type, false)
				} else {
					values[1] = sqltype.Null(f.parent.columns[1].Usage.Type)
				}
				f.store.versions = append(f.store.versions, rowVersion{row: RowID(i + 2), version: VersionID(i + 2), born: 1, values: values})
			}
			f.e.nextRow, f.e.nextVersion, f.e.commitSeq = 3, 3, 1
			root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.parent, &definition, f.store, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.e.indexRoots[f.index.address] = root
			want := map[RowID]int{1: 0}
			if !partial {
				want[2] = 1
				want[3] = 2
			}
			insertConflictWantHints(t, root, f.store, want)
			scope, ref := insertConflictUnitScope(t, f)
			// Real row publication preparation supplies replacement/append slots. The
			// independently stated physical positions below cannot be sorted ordinals.
			publish := func(writes []rowVersion, deleted RowID) {
				t.Helper()
				p := rowPublication{address: f.parent.address, relation: f.parent, store: f.store, root: f.store.versions, ends: f.store.ends, writes: writes, singleSlot: -1}
				if deleted != 0 {
					old, position, ok := rowByID(p.root, deleted, -1)
					if !ok {
						t.Fatal("missing deletion", deleted)
					}
					p.deletion = &privateDeletionRoot{rows: map[RowID]deleteCandidate{deleted: {ref: ref, storage: f.store, row: deleted, version: old.version, ordinal: position}}}
				}
				if err := f.e.prepareRowPublication(&p, scope.txRef, true); err != nil {
					t.Fatal(err)
				}
				next, err := mergeIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.parent, f.index, root.entries, &p, false, true, false, "public", scope.output, nil)
				if err != nil {
					t.Fatal(err)
				}
				if next != nil {
					root = next
				}
				for i, row := range writes {
					row.born = 2
					row.base = 0
					p.next[publicationSlot(&p, i)] = row
					f.e.nextRow = max(f.e.nextRow, uint64(row.row))
					f.e.nextVersion = max(f.e.nextVersion, uint64(row.version))
				}
				f.e.commitSeq = 2
				f.store = &relationStorage{versions: p.next, ends: p.nextEnds}
				if p.nextEnds == nil {
					f.store.ends = p.ends
				}
				f.e.storage[f.parent.address] = f.store
				f.e.indexRoots[f.index.address] = root
				insertConflictWantHints(t, root, f.store, want)
			}
			want[4] = 3
			publish([]rowVersion{{row: 4, version: 4, values: insertConflictUnitRow(t, f, 5)}}, 0)
			want[2] = 1
			publish([]rowVersion{{row: 2, version: 5, base: 2, values: insertConflictUnitRow(t, f, 20)}}, 0)
			want[3] = 2
			publish([]rowVersion{{row: 3, version: 6, base: 3, values: insertConflictUnitRow(t, f, 7)}}, 0)
			values := insertConflictUnitRow(t, f, 10)
			values[1] = sqltype.Boolean(f.parent.columns[1].Usage.Type, false)
			if partial {
				delete(want, 1)
			}
			publish([]rowVersion{{row: 1, version: 7, base: 1, values: values}}, 0)
			delete(want, 2)
			publish(nil, 2)
			rebuilt, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.parent, &definition, f.store, nil)
			if err != nil {
				t.Fatal(err)
			}
			insertConflictWantHints(t, rebuilt, f.store, want)
			// Parent shape replacement preserves slots, including the dead row2 hole.
			parent := *f.parent
			parent.revision++
			parent.attributes = append(slices.Clone(parent.attributes), 30)
			parent.attributeHighWater = 30
			parent.columns = append(slices.Clone(parent.columns), ColumnDescriptor{Name: "payload", Usage: parent.columns[0].Usage})
			parent.index = maps.Clone(parent.index)
			parent.index["payload"] = 2
			shaped := *f.store
			shaped.versions = slices.Clone(f.store.versions)
			for i := range shaped.versions {
				shaped.versions[i].values = append(slices.Clone(shaped.versions[i].values), sqltype.Null(parent.columns[2].Usage.Type))
			}
			index := *f.index
			index.revision++
			shapedDefinition := definition
			if definition.predicate != nil {
				predicate := *definition.predicate
				predicate.attributes = parent.attributes
				shapedDefinition.predicate = &predicate
			}
			index.sqlIndex = &shapedDefinition
			nextCatalog := *f.e.state
			nextCatalog.descriptors = maps.Clone(nextCatalog.descriptors)
			nextCatalog.descriptors[parent.address] = &parent
			nextCatalog.descriptors[index.address] = &index
			stores := maps.Clone(f.e.storage)
			stores[parent.address] = &shaped
			prepared, err := f.e.prepareIndexCatalog(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.e.state, &nextCatalog, stores, f.e.indexRoots, map[ObjectAddress]bool{parent.address: true}, nil, scope.output, nil, nil, nil)
			if err != nil {
				t.Fatal("trusted shape reconstruction", err)
			}
			insertConflictWantHints(t, prepared[index.address], &shaped, want)
			changes := &catalogChanges{descriptors: map[ObjectAddress]Descriptor{index.address: &index}, originals: map[ObjectAddress]Descriptor{index.address: f.index}}
			if err := prepareAlterIndexRoots(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: f.e.CatalogRead()}, changes, f.e.CatalogRead(), &parent, &shaped, nil, false); err != nil {
				t.Fatal("private reconstruction", err)
			}
			insertConflictWantHints(t, changes.uniqueRoots[index.address], &shaped, want)
		})
	}
}

func TestInsertConflictIndexPositionHintsConsumerFences(t *testing.T) {
	for i, isolation := range []Isolation{ReadUncommitted, ReadCommitted, RepeatableRead, Serializable} {
		t.Run([]string{"read-uncommitted", "read-committed", "repeatable-read", "serializable"}[i], func(t *testing.T) {
			f := insertConflictUniqueFixture(t)
			scope, ref := insertConflictUnitScope(t, f)
			scope.owner.isolation = isolation
			scope.owner.frontier = 1
			root := *f.e.indexRoots[f.index.address]
			root.entries = slices.Clone(root.entries)
			root.entries[0].position = -1
			f.e.indexRoots[f.index.address] = &root
			admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 10)}, indexWriteBatch{}, nil)
			if stableIsolation(isolation) {
				if admitted || !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal("stable identity fence", admitted, err)
				}
			} else if admitted || err != nil {
				t.Fatal("nonstable omission must not recover version", admitted, err)
			}
			scope.insertConflict = nil
			err = scope.checkUniqueRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 10)}, nil, indexWriteBatch{})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
				t.Fatal("ordinary enforcement must not recover version", err)
			}
		})
	}
	f := insertConflictUniqueFixture(t)
	entry := f.e.indexRoots[f.index.address].entries[0]
	for _, change := range []func(*logicalIndexEntry){func(e *logicalIndexEntry) { e.position = -1 }, func(e *logicalIndexEntry) { e.position = len(f.store.versions) }, func(e *logicalIndexEntry) { e.row++ }, func(e *logicalIndexEntry) { e.version++ }} {
		bad := entry
		change(&bad)
		if _, err := conflictEntryVersion(f.store.versions, bad); !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal("invalid hint silently recovered", bad, err)
		}
	}
}

func TestInsertConflictIndexPositionHintsReusableTypeBaseline(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	f.store.versions = append(f.store.versions, rowVersion{row: 2, version: 2, born: 2, values: insertConflictUnitRow(t, f, 20)})
	f.store.ends = &deletionRoot{rows: map[RowID]rowEnd{1: {version: 1}}}
	f.e.nextRow, f.e.nextVersion, f.e.commitSeq = 2, 2, 2
	authOp, done := roleOperation(t, f.e, "postgres")
	defer done()
	auth, err := authOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validateAlterIndexBuilds(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: f.e.CatalogRead()}, auth, f.e.CatalogRead(), f.parent, f.store, nil, nil, "public", sqltype.OutputEnvironment{}, []columnTransformation{{attribute: 10, rewrite: true}}, nil, nil, true)
	if err != nil || validated[f.index.name] == nil {
		t.Fatal("no-overlay type validation", validated, err)
	}
	index := *f.index
	index.revision++
	changes := &catalogChanges{descriptors: map[ObjectAddress]Descriptor{index.address: &index}, originals: map[ObjectAddress]Descriptor{index.address: f.index}}
	if err := prepareAlterIndexRoots(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: f.e.CatalogRead()}, changes, f.e.CatalogRead(), f.parent, f.store, validated, false); err != nil {
		t.Fatal(err)
	}
	root := changes.uniqueRoots[index.address]
	if root != validated[index.name].root {
		t.Fatal("validated root was not genuinely reused")
	}
	insertConflictWantHints(t, root, f.store, map[RowID]int{2: 1})
	scope, ref := insertConflictUnitScope(t, f)
	scope.owner.catalog = changes
	scope.owner.isolation = RepeatableRead
	scope.owner.frontier = 2
	if admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 20)}, indexWriteBatch{}, nil); err != nil || admitted {
		t.Fatal("reused tombstone baseline did not recover actual visible born", admitted, err)
	}
	scope.owner.frontier = 1
	admitted, visibilityErr := scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 20)}, indexWriteBatch{}, nil)
	wantDiagnostic := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"}
	var visibilityDiagnostic *sqlstate.Error
	if admitted || !errors.As(visibilityErr, &visibilityDiagnostic) || !reflect.DeepEqual(*visibilityDiagnostic, wantDiagnostic) {
		t.Fatal("reused root lost actual post-frontier born", admitted, visibilityErr)
	}
	// Root/slice and keys retain their immutable data through retrieval.
	row, err := conflictEntryVersion(f.store.versions, root.entries[0])
	if err != nil || row.born != 2 || !reflect.DeepEqual(row.values, insertConflictUnitRow(t, f, 20)) {
		t.Fatal("wrong born or data from reused root", row, err)
	}
}
