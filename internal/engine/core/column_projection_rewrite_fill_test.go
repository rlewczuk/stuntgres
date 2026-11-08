package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCatalogOverlayRewriteAddedColumnFills(t *testing.T) {
	for _, combined := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			for _, restore := range []bool{false, true} {
				name := map[bool]string{false: "equal_attributes", true: "type_and_add"}[combined]
				name += "/" + map[bool]string{false: "empty", true: "populated"}[populated]
				name += "/" + map[bool]string{false: "commit", true: "savepoint"}[restore]
				t.Run(name, func(t *testing.T) {
					e := engine.New()
					owner := enginetest.StartSession(t, e, nil)
					ref := enginetest.TransactionBegin(t, owner)
					op := enginetest.TransactionOperation(t, owner)
					ended := false
					var active *core.StatementScope
					defer func() {
						if active != nil {
							active.Finish(errors.New("unfinished rewrite fill unit"))
						}
						op.Done()
						if !ended {
							enginetest.TransactionEnd(t, owner, ref, false)
						}
					}()
					before := e.CatalogRead()
					int4 := enginetest.Usage(e, sqltype.Int4)
					int8 := enginetest.Usage(e, sqltype.Int8)
					float8 := enginetest.Usage(e, sqltype.Float8)
					integer := func(usage sqltype.Usage, n int64) sqltype.Value {
						t.Helper()
						v, err := sqltype.Integer(usage.Type, n)
						if err != nil {
							t.Fatal(err)
						}
						return v
					}
					minusOne, seven, nine := integer(int8, -1), integer(int4, 7), integer(int4, 9)
					converted, err := sqltype.Float(float8.Type, 6)
					if err != nil {
						t.Fatal(err)
					}
					start := func() *core.StatementScope {
						t.Helper()
						scope, err := e.StartStatement(op)
						if err != nil {
							t.Fatal(err)
						}
						active = scope
						t.Cleanup(func() { scope.Finish(errors.New("unfinished rewrite fill unit")) })
						if err := scope.Gate(&parser.CreateTable{}); err != nil {
							t.Fatal(err)
						}
						return scope
					}
					publish := func(scope *core.StatementScope, stage *core.CatalogStage) {
						t.Helper()
						if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
							t.Fatal(err)
						}
						// Use the ordinary validated-stage overlay owner directly, so
						// the assertions cover both recording and core projection.
						if err := e.StageCatalogOverlay(scope, stage, scope.PendingCatalog().Clone()); err != nil {
							t.Fatal(err)
						}
						if err := scope.Finish(nil); err != nil {
							t.Fatal(err)
						}
						active = nil
					}

					// The same real session/stage/row-type setup as the owning
					// transactional schema publication tests, including NO DATA.
					scope := start()
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(op)
					if err != nil {
						e.Mu.Unlock()
						t.Fatal(err)
					}
					stage := e.BeginCatalogStage(auth.Catalog)
					stage.AuthorizeLocked(auth, op.Session)
					d, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "overlay_rewrite_fill", auth.State.CurrentUser, []catalog.Column{{Name: "v", Usage: int4, Nullable: true, NullabilityKnown: true}}, map[string]int{"v": 0})
					if err == nil {
						d.TableMethod = 2
						err = e.StageRowTypeLocked(stage, d)
					}
					if err == nil {
						err = stage.Create(d)
					}
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					var initial [][]sqltype.Value
					if populated {
						initial = [][]sqltype.Value{{seven}}
					}
					if err := e.StageQueryTableRows(context.Background(), stage, d, initial, ref); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { e.DiscardCatalogStage(stage) })
					publish(scope, stage)
					address := d.Address()
					txOwner := scope.Owner
					assertPrivate := func() {
						t.Helper()
						if before.Get(address) != nil || e.CatalogRead().Get(address) != nil || e.Storage.Relations[address] != nil {
							t.Fatal("private rewrite published catalog or storage before commit")
						}
					}
					assertPrivate()

					// Build every subsequent descriptor and RowShape through a real
					// stage based on the current immutable transaction catalog.
					alter := func(next *catalog.Relation, replacement *storage.Relation, rewritten, ownProjected bool, fills map[int32]sqltype.Value) {
						t.Helper()
						scope := start()
						e.Mu.Lock()
						base := e.PrivateCatalogLocked(txOwner.Catalog)
						original := e.PrivateStorageLocked(txOwner.Catalog, address)
						auth, err := e.AuthorizationLocked(op)
						if err != nil {
							e.Mu.Unlock()
							t.Fatal(err)
						}
						stage := e.BeginCatalogStage(base)
						stage.AuthorizeLocked(auth, op.Session)
						err = stage.Replace(next)
						stage.RowShapes = map[ids.ObjectAddress]storage.RowShape{address: {
							Original: original, Root: original.Versions, Replacement: replacement,
							ObservedLength: len(original.Versions), ObservedEnds: original.Ends,
							Generation: original.Generation, Rewritten: rewritten,
							OwnProjected: ownProjected, Frontier: e.Visibility.CommitSeq(), Fills: fills,
						}}
						e.Mu.Unlock()
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { e.DiscardCatalogStage(stage) })
						if rewritten {
							// A complete rewrite has consumed the earlier private rows.
							scope.StageProjectedColumnRoots(address, nil, nil)
						}
						publish(scope, stage)
						assertPrivate()
						if txOwner.Catalog.Storage[address].Replacement != replacement {
							t.Fatal("publication lost the exact replacement heap")
						}
					}
					clone := func(old *catalog.Relation) *catalog.Relation {
						next := *old
						next.ObjectRevision = old.Revision() + 1
						next.Columns = slices.Clone(old.Columns)
						next.Attributes = slices.Clone(old.Attributes)
						next.Index = maps.Clone(old.Index)
						return &next
					}
					add := func(next *catalog.Relation, name string, usage sqltype.Usage) int32 {
						next.ColumnMeta.HighWater++
						attribute := next.ColumnMeta.HighWater
						next.Index[name] = len(next.Columns)
						next.Columns = append(next.Columns, catalog.Column{Name: name, Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: uint32(address.ObjectID), OriginAttribute: int16(attribute)})
						next.Attributes = append(next.Attributes, attribute)
						return attribute
					}
					withLatency := clone(d)
					latency := add(withLatency, "latency", int8)
					original := txOwner.Catalog.Storage[address].Replacement
					alter(withLatency, original, false, false, map[int32]sqltype.Value{latency: minusOne})

					// Retain independently saved map and snapshot backing, as well as
					// the journal and an actual savepoint that shares those roots.
					txOwner.Mu.Lock()
					prior := txOwner.Catalog.Clone()
					priorShape := prior.Storage[address]
					priorShape.Snapshot = slices.Clone(txOwner.Writes[address])
					priorShape.HasSnapshot = true
					prior.Storage[address] = priorShape
					txOwner.Catalog = prior
					mark := core.CaptureSavepoint(txOwner, "before_rewrite")
					txOwner.Mu.Unlock()
					savedFills, savedSnapshot := priorShape.Fills, priorShape.Snapshot
					snapshotCopy := slices.Clone(savedSnapshot)
					for i := range snapshotCopy {
						snapshotCopy[i].Values = slices.Clone(snapshotCopy[i].Values)
					}
					assertEarlier := func() {
						t.Helper()
						shape := prior.Storage[address]
						if mark.Catalog != prior || shape.Replacement != original || !shape.HasSnapshot || shape.Rewritten || !reflect.DeepEqual(shape.Snapshot, snapshotCopy) || !reflect.DeepEqual(savedSnapshot, snapshotCopy) {
							t.Fatal("rewrite mutated an earlier journal/savepoint/snapshot")
						}
						if len(savedFills) != 1 || savedFills[latency].Type() != int8.Type || !reflect.DeepEqual(savedFills, shape.Fills) {
							t.Fatal("rewrite mutated predecessor fill map")
						}
						if n, err := savedFills[latency].Integer(); err != nil || n != -1 {
							t.Fatal("predecessor fill changed", n, err)
						}
					}

					next := clone(withLatency)
					next.Columns[1].Usage = float8
					materializedFills := map[int32]sqltype.Value{latency: minusOne}
					values := []sqltype.Value{seven, converted}
					if combined {
						marker := add(next, "marker", int4)
						materializedFills[marker] = seven
						values = append(values, seven)
					}
					replacement := &storage.Relation{}
					if populated {
						row := txOwner.Writes[address][0]
						row.Values = values
						replacement.Versions = []storage.RowVersion{row}
					}
					alter(next, replacement, true, true, materializedFills)
					assertRetired := func() {
						t.Helper()
						shape := txOwner.Catalog.Storage[address]
						if shape.Fills != nil || !shape.Rewritten || shape.HasSnapshot || shape.Snapshot != nil || len(txOwner.Writes[address]) != 0 {
							t.Fatal("rewrite retained fills, snapshot or consumed private rows", shape)
						}
						assertEarlier()
					}
					assertRetired()
					if len(materializedFills) != len(values)-1 || materializedFills[latency].Type() != int8.Type {
						t.Fatal("publication changed the incoming materialized fills")
					}

					// Rewritten is cumulative in StorageChange, but the following
					// ordinary ADD must still establish a fresh missing-value fill.
					withTail := clone(next)
					tail := add(withTail, "tail", int4)
					projected := *replacement
					projected.Versions = slices.Clone(replacement.Versions)
					for i := range projected.Versions {
						projected.Versions[i].Values = append(slices.Clone(replacement.Versions[i].Values), nine)
					}
					alter(withTail, &projected, false, true, map[int32]sqltype.Value{tail: nine})
					addedJournal := txOwner.Catalog
					addedShape := addedJournal.Storage[address]
					freshFills := addedShape.Fills
					if !addedShape.Rewritten || len(freshFills) != 1 || freshFills[tail].Type() != int4.Type {
						t.Fatal("cumulative rewrite suppressed the later ADD fill", addedShape)
					}
					if n, err := freshFills[tail].Integer(); err != nil || n != 9 {
						t.Fatal(n, err)
					}

					final := clone(withTail)
					final.Columns[len(final.Columns)-1].Usage = int8
					finalStore := projected
					finalStore.Versions = slices.Clone(projected.Versions)
					for i := range finalStore.Versions {
						finalStore.Versions[i].Values = slices.Clone(projected.Versions[i].Values)
						finalStore.Versions[i].Values[len(final.Columns)-1] = integer(int8, 9)
					}
					alter(final, &finalStore, true, true, freshFills)
					assertRetired()
					if len(freshFills) != 1 || freshFills[tail].Type() != int4.Type || !reflect.DeepEqual(addedJournal.Storage[address].Fills, freshFills) || addedJournal.Storage[address].Replacement != &projected {
						t.Fatal("later rewrite mutated the intervening ADD journal")
					}
					if n, err := freshFills[tail].Integer(); err != nil || n != 9 {
						t.Fatal("later rewrite changed the saved ADD fill", n, err)
					}
					wantRewrite := []sqltype.Value{seven, converted}
					if combined {
						wantRewrite = append(wantRewrite, seven)
					}
					wantFinal := append(slices.Clone(wantRewrite), integer(int8, 9))
					wantCount := 0
					if populated {
						wantCount = 1
					}
					for _, check := range []struct {
						store  *storage.Relation
						values []sqltype.Value
					}{
						{replacement, wantRewrite},
						{&projected, append(slices.Clone(wantRewrite), nine)},
						{&finalStore, wantFinal},
					} {
						if len(check.store.Versions) != wantCount {
							t.Fatal("replacement changed row count", len(check.store.Versions), wantCount)
						}
						for _, row := range check.store.Versions {
							if !reflect.DeepEqual(row.Values, check.values) {
								t.Fatal("replacement values/types changed", row.Values, check.values)
							}
						}
					}

					if restore {
						for range 2 {
							txOwner.Mu.Lock()
							core.RestoreSavepoint(txOwner, mark, mark.Settings.Clone())
							txOwner.Mu.Unlock()
							e.Locks.Restore(scope.LockTicket, mark.Locks)
						}
						assertEarlier()
						restored := txOwner.Catalog.Storage[address]
						if restored.Relation != withLatency || restored.Replacement != original || !restored.HasSnapshot || !reflect.DeepEqual(restored.Fills, savedFills) || !reflect.DeepEqual(restored.Snapshot, snapshotCopy) || !reflect.DeepEqual(txOwner.Writes[address], snapshotCopy) {
							t.Fatal("savepoint failed to restore predecessor catalog and private rows")
						}
						assertPrivate()
					}
					op.Done()
					enginetest.TransactionEnd(t, owner, ref, true)
					ended = true
					wantDescriptor := final
					wantRows := finalStore.Versions
					if restore {
						wantDescriptor, wantRows = withLatency, snapshotCopy
					}
					committed := e.CatalogRead().MustRelation(address)
					store := e.Storage.Relations[address]
					if !reflect.DeepEqual(committed.Columns, wantDescriptor.Columns) || !slices.Equal(committed.Attributes, wantDescriptor.Attributes) || store == nil || len(store.Versions) != len(wantRows) {
						t.Fatal("commit lost the selected descriptor/heap root")
					}
					for i, row := range store.Versions {
						if !reflect.DeepEqual(row.Values, wantRows[i].Values) {
							t.Fatal("commit changed replacement values", row.Values, wantRows[i].Values)
						}
					}
				})
			}
		}
	}
}
