package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func insertConflictWantHints(t *testing.T, root *storage.IndexRoot, store *storage.Relation, want map[ids.RowID]int) {
	t.Helper()
	if root == nil || len(root.Entries) != len(want) {
		t.Fatal("wrong hint membership", root, want)
	}
	for _, entry := range root.Entries {
		position, ok := want[entry.Row]
		if !ok || entry.Position != position {
			t.Fatal("wrong actual physical slot", entry, want)
		}
		row, err := conflictEntryVersion(store.Versions, entry)
		if err != nil || row.Row != entry.Row || row.Version != entry.Version {
			t.Fatal("invalid immutable root/storage pair", entry, err)
		}
		if len(row.Values) != 0 && &row.Values[0] != &store.Versions[position].Values[0] {
			t.Fatal("version recovery copied values")
		}
		if root.Membership != nil && root.Membership[entry.Row] != entry.Version {
			t.Fatal("partial membership lost identity", entry)
		}
	}
}

func TestInsertConflictIndexPositionHints(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonpartial", true: "partial"}[partial], func(t *testing.T) {
			f := insertConflictUniqueFixture(t)
			definition := *f.index.SQLIndex
			if !partial {
				definition.Predicate = nil
			}
			f.index.SQLIndex = &definition
			for i, k := range []int64{20, 30} {
				values := insertConflictUnitRow(t, f, k)
				if i == 0 {
					values[1] = sqltype.Boolean(f.parent.Columns[1].Usage.Type, false)
				} else {
					values[1] = sqltype.Null(f.parent.Columns[1].Usage.Type)
				}
				f.store.Versions = append(f.store.Versions, storage.RowVersion{Row: ids.RowID(i + 2), Version: ids.VersionID(i + 2), Born: 1, Values: values})
			}
			f.e.storage.SetFrontierLocked(3, 3)
			f.e.visibility.SetCommitSeqLocked(1)
			root, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.e.mu.Lock()
			f.e.storage.InstallIndexRootLocked(f.index.Address(), root)
			f.e.mu.Unlock()
			want := map[ids.RowID]int{1: 0}
			if !partial {
				want[2] = 1
				want[3] = 2
			}
			insertConflictWantHints(t, root, f.store, want)
			scope, ref := insertConflictUnitScope(t, f)
			// Real row publication preparation supplies replacement/append slots. The
			// independently stated physical positions below cannot be sorted ordinals.
			publish := func(writes []storage.RowVersion, deleted ids.RowID) {
				t.Helper()
				p := rowPublication{address: f.parent.Address(), relation: f.parent, store: f.store, root: f.store.Versions, ends: f.store.Ends, writes: writes, singleSlot: -1}
				if deleted != 0 {
					old, position, ok := rowByID(p.root, deleted, -1)
					if !ok {
						t.Fatal("missing deletion", deleted)
					}
					p.deletion = &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{deleted: {Ref: ref, Storage: f.store, Row: deleted, Version: old.Version, Ordinal: position}}}
				}
				if err := f.e.prepareRowPublication(&p, scope.txRef, true); err != nil {
					t.Fatal(err)
				}
				next, err := mergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, f.index, root.Entries, &p, false, true, false, "public", scope.output, nil)
				if err != nil {
					t.Fatal(err)
				}
				if next != nil {
					root = next
				}
				for i, row := range writes {
					row.Born = 2
					row.Base = 0
					p.next[publicationSlot(&p, i)] = row
					f.e.storage.SetFrontierLocked(max(f.e.storage.RowFrontier(), uint64(row.Row)), max(f.e.storage.VersionFrontier(), uint64(row.Version)))
				}
				f.e.visibility.SetCommitSeqLocked(2)
				f.store = &storage.Relation{Versions: p.next, Ends: p.nextEnds}
				if p.nextEnds == nil {
					f.store.Ends = p.ends
				}
				f.e.mu.Lock()
				f.e.storage.InstallRelationLocked(f.parent.Address(), f.store)
				f.e.mu.Unlock()
				f.e.mu.Lock()
				f.e.storage.InstallIndexRootLocked(f.index.Address(), root)
				f.e.mu.Unlock()
				insertConflictWantHints(t, root, f.store, want)
			}
			want[4] = 3
			publish([]storage.RowVersion{{Row: 4, Version: 4, Values: insertConflictUnitRow(t, f, 5)}}, 0)
			want[2] = 1
			publish([]storage.RowVersion{{Row: 2, Version: 5, Base: 2, Values: insertConflictUnitRow(t, f, 20)}}, 0)
			want[3] = 2
			publish([]storage.RowVersion{{Row: 3, Version: 6, Base: 3, Values: insertConflictUnitRow(t, f, 7)}}, 0)
			values := insertConflictUnitRow(t, f, 10)
			values[1] = sqltype.Boolean(f.parent.Columns[1].Usage.Type, false)
			if partial {
				delete(want, 1)
			}
			publish([]storage.RowVersion{{Row: 1, Version: 7, Base: 1, Values: values}}, 0)
			delete(want, 2)
			publish(nil, 2)
			rebuilt, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
			if err != nil {
				t.Fatal(err)
			}
			insertConflictWantHints(t, rebuilt, f.store, want)
			// Parent shape replacement preserves slots, including the dead row2 hole.
			parent := *f.parent
			parent.ObjectRevision++
			parent.Attributes = append(slices.Clone(parent.Attributes), 30)
			parent.ColumnMeta.HighWater = 30
			parent.Columns = append(slices.Clone(parent.Columns), catalog.Column{Name: "payload", Usage: parent.Columns[0].Usage})
			parent.Index = maps.Clone(parent.Index)
			parent.Index["payload"] = 2
			shaped := *f.store
			shaped.Versions = slices.Clone(f.store.Versions)
			for i := range shaped.Versions {
				shaped.Versions[i].Values = append(slices.Clone(shaped.Versions[i].Values), sqltype.Null(parent.Columns[2].Usage.Type))
			}
			index := *f.index
			index.ObjectRevision++
			shapedDefinition := definition
			if definition.Predicate != nil {
				predicate := *definition.Predicate
				predicate.Attributes = parent.Attributes
				shapedDefinition.Predicate = &predicate
			}
			index.SQLIndex = &shapedDefinition
			nextCatalog := *f.e.catalog.state
			nextCatalog.CloneDescriptors()
			nextCatalog.SetDescriptor(parent.Address(), &parent)
			nextCatalog.SetDescriptor(index.Address(), &index)
			stores := maps.Clone(f.e.storage.Relations)
			stores[parent.Address()] = &shaped
			prepared, err := f.e.prepareIndexCatalog(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.e.catalog.state, &nextCatalog, stores, f.e.storage.IndexRoots, map[ids.ObjectAddress]bool{parent.Address(): true}, nil, scope.output, nil, nil, nil)
			if err != nil {
				t.Fatal("trusted shape reconstruction", err)
			}
			insertConflictWantHints(t, prepared[index.Address()], &shaped, want)
			changes := &catalogChanges{descriptors: map[ids.ObjectAddress]catalog.Descriptor{index.Address(): &index}, originals: map[ids.ObjectAddress]catalog.Descriptor{index.Address(): f.index}}
			if err := prepareAlterIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: f.e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, changes, f.e.CatalogRead(), &parent, &shaped, nil, false); err != nil {
				t.Fatal("private reconstruction", err)
			}
			insertConflictWantHints(t, changes.indexesOrZero().uniqueRoots[index.Address()], &shaped, want)
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
			root := *f.e.storage.IndexRoots[f.index.Address()]
			root.Entries = slices.Clone(root.Entries)
			root.Entries[0].Position = -1
			f.e.mu.Lock()
			f.e.storage.InstallIndexRootLocked(f.index.Address(), &root)
			f.e.mu.Unlock()
			admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 10)}, indexWriteBatch{}, nil)
			if stableIsolation(isolation) {
				if admitted || !errors.Is(err, ids.ErrCatalogReplaced) {
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
	entry := f.e.storage.IndexRoots[f.index.Address()].Entries[0]
	for _, change := range []func(*storage.IndexEntry){func(e *storage.IndexEntry) { e.Position = -1 }, func(e *storage.IndexEntry) { e.Position = len(f.store.Versions) }, func(e *storage.IndexEntry) { e.Row++ }, func(e *storage.IndexEntry) { e.Version++ }} {
		bad := entry
		change(&bad)
		if _, err := conflictEntryVersion(f.store.Versions, bad); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("invalid hint silently recovered", bad, err)
		}
	}
}

func TestInsertConflictIndexPositionHintsReusableTypeBaseline(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	f.store.Versions = append(f.store.Versions, storage.RowVersion{Row: 2, Version: 2, Born: 2, Values: insertConflictUnitRow(t, f, 20)})
	f.store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 1}}}
	f.e.storage.SetFrontierLocked(2, 2)
	f.e.visibility.SetCommitSeqLocked(2)
	authOp, done := roleOperation(t, f.e, "postgres")
	defer done()
	auth, err := authOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validateAlterIndexBuilds(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: f.e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, auth, f.e.CatalogRead(), f.parent, f.store, nil, nil, "public", sqltype.OutputEnvironment{}, []columnTransformation{{attribute: 10, rewrite: true}}, nil, nil, true)
	if err != nil || validated[f.index.Name()] == nil {
		t.Fatal("no-overlay type validation", validated, err)
	}
	index := *f.index
	index.ObjectRevision++
	changes := &catalogChanges{descriptors: map[ids.ObjectAddress]catalog.Descriptor{index.Address(): &index}, originals: map[ids.ObjectAddress]catalog.Descriptor{index.Address(): f.index}}
	if err := prepareAlterIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: f.e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, changes, f.e.CatalogRead(), f.parent, f.store, validated, false); err != nil {
		t.Fatal(err)
	}
	root := changes.indexesOrZero().uniqueRoots[index.Address()]
	if root != validated[index.Name()].root {
		t.Fatal("validated root was not genuinely reused")
	}
	insertConflictWantHints(t, root, f.store, map[ids.RowID]int{2: 1})
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
	row, err := conflictEntryVersion(f.store.Versions, root.Entries[0])
	if err != nil || row.Born != 2 || !reflect.DeepEqual(row.Values, insertConflictUnitRow(t, f, 20)) {
		t.Fatal("wrong born or data from reused root", row, err)
	}
}
