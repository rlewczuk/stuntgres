package core_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"testing"
	"time"
)

func ginCoreMaterializedPersistence(t *testing.T) {
	source := engine.New()
	actor := enginetest.StartSession(t, source, nil)
	for _, q := range []string{
		"CREATE TABLE gin_mv_base(id integer,a integer[])", "INSERT INTO gin_mv_base VALUES(1,'{2}'),(2,'{2}')",
		"CREATE MATERIALIZED VIEW gin_mv AS SELECT id,a FROM gin_mv_base", "CREATE UNIQUE INDEX gin_mv_u ON gin_mv(id)",
		"CREATE INDEX gin_mv_i ON gin_mv USING gin(a)", "CREATE INDEX gin_mv_e ON gin_mv USING gin((a||ARRAY[9]))",
		"CREATE MATERIALIZED VIEW gin_mv_dup AS SELECT a FROM gin_mv_base", "CREATE INDEX gin_mv_dup_i ON gin_mv_dup USING gin(a)",
		"CREATE MATERIALIZED VIEW gin_mv_empty AS SELECT id,a FROM gin_mv_base WHERE id<0", "CREATE INDEX gin_mv_empty_i ON gin_mv_empty USING gin(a)",
		"CREATE MATERIALIZED VIEW gin_mv_unpop AS SELECT id,a FROM gin_mv_base WITH NO DATA", "CREATE INDEX gin_mv_unpop_i ON gin_mv_unpop USING gin(a)",
		"INSERT INTO gin_mv_base VALUES(3,'{2}')", "REFRESH MATERIALIZED VIEW CONCURRENTLY gin_mv", "REFRESH MATERIALIZED VIEW CONCURRENTLY gin_mv",
		"ALTER INDEX gin_mv_i SET(fastupdate=off)", "INSERT INTO gin_mv_base VALUES(4,'{2}')", "REFRESH MATERIALIZED VIEW CONCURRENTLY gin_mv",
		"BEGIN", "REFRESH MATERIALIZED VIEW CONCURRENTLY gin_mv", "COMMIT",
	} {
		enginetest.SessionSQL(t, actor, q)
	}
	check := func(e *core.Engine) {
		t.Helper()
		idx := ginCoreRelation(t, e, "gin_mv_i")
		ginCorePending(t, e, idx, 1, 4)
		if !slices.Equal(ginCorePlacementIDs(t, e, idx, false), []int64{1, 2, 4}) || !slices.Equal(ginCorePlacementIDs(t, e, idx, true), []int64{3}) {
			t.Fatal("concurrent retained/minted placement lost")
		}
		expression := ginCoreRelation(t, e, "gin_mv_e")
		ginCorePending(t, e, expression, 4, 4)
		if !slices.Equal(ginCorePlacementIDs(t, e, expression, true), []int64{3, 4}) {
			t.Fatal("independent expression placement lost")
		}
		ginCorePending(t, e, ginCoreRelation(t, e, "gin_mv_dup_i"), 0, 2)
		ginCorePending(t, e, ginCoreRelation(t, e, "gin_mv_empty_i"), 0, 0)
		ginCorePending(t, e, ginCoreRelation(t, e, "gin_mv_unpop_i"), 0, 0)
		if ginCoreRelation(t, e, "gin_mv_unpop").Populated || !ginCoreRelation(t, e, "gin_mv_empty").Populated {
			t.Fatal("snapshot population changed")
		}
		ginCoreMaterializedUnique(t, e, 4)
		for _, name := range []string{"gin_mv_i", "gin_mv_e", "gin_mv_dup_i", "gin_mv_empty_i", "gin_mv_unpop_i"} {
			target := ginCoreRelation(t, e, name)
			root := e.Storage.IndexRoots[target.Address()]
			store := e.Storage.Relations[target.SQLIndex.Parent]
			parent, ok := e.CatalogRead().Relation(target.SQLIndex.Parent)
			if !ok {
				t.Fatal("missing materialized parent")
			}
			bindings, err := index.BindGinIndex(e.CatalogRead(), parent, target.SQLIndex)
			if err != nil {
				t.Fatal(err)
			}
			for _, columns := range [2][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
				for ordinal, column := range columns {
					if !bindings[ordinal].Same(column.Binding) {
						t.Fatal("materialized destination binding stale")
					}
					for _, token := range column.Tokens {
						for _, posting := range token.Postings {
							if posting.Position < 0 || posting.Position >= len(store.Versions) {
								t.Fatal("invalid materialized posting slot")
							}
							row := store.Versions[posting.Position]
							if row.Row != posting.Row || row.Version != posting.Version {
								t.Fatal("source posting identity retained")
							}
						}
					}
				}
			}
			for _, entry := range root.Entries {
				row := store.Versions[entry.Position]
				if row.Row != entry.Row || row.Version != entry.Version {
					t.Fatal("materialized destination identity was inferred")
				}
			}
		}
	}
	check(source)
	original := ginCorePending(t, source, ginCoreRelation(t, source, "gin_mv_i"), 1, 4)
	payload := ginCoreCopyPayload(original.Gin)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	independent := engine.New()
	if err := engine.Restore(context.Background(), independent, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(independent)
	if err := engine.Restore(context.Background(), source, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(source)
	if !reflect.DeepEqual(original.Gin, payload) {
		t.Fatal("same restore mutated original split")
	}
	state := source.CatalogRead().State()
	roots := maps.Clone(source.Storage.IndexRoots)
	stores := maps.Clone(source.Storage.Relations)
	marker := ""
	for _, line := range strings.Split(dump.String(), "\n") {
		if strings.HasPrefix(line, "-- stuntgres-gin-index-v1 ") {
			marker = line
			break
		}
	}
	if marker == "" {
		t.Fatal("materialized split metadata absent")
	}
	if err := engine.Restore(context.Background(), source, strings.NewReader(dump.String()+"\n"+marker+"\n")); err == nil {
		t.Fatal("late duplicate materialized metadata accepted")
	}
	if source.CatalogRead().State() != state || !maps.Equal(roots, source.Storage.IndexRoots) || !maps.Equal(stores, source.Storage.Relations) {
		t.Fatal("failed materialized split restore changed destination")
	}
	pause := util.NewExecutionPause("gin.restore.prepared")
	defer pause.Resume()
	ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.Restore(ctx, source, bytes.NewReader(dump.Bytes())) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		pause.Resume()
		if !joined {
			<-done
		}
	})
	ginCoreAwaitPause(t, pause)
	cancel()
	pause.Resume()
	err := <-done
	joined = true
	if !errors.Is(err, context.Canceled) || source.CatalogRead().State() != state || !maps.Equal(roots, source.Storage.IndexRoots) || !maps.Equal(stores, source.Storage.Relations) {
		t.Fatal("canceled materialized split restore changed destination", err)
	}
	t.Run("same-restored/direct-clean-refresh", func(t *testing.T) { ginCoreMaterializedCleanRefresh(t, source, false) })
	t.Run("independent-restored/private-clean-refresh", func(t *testing.T) { ginCoreMaterializedCleanRefresh(t, independent, true) })
	other := enginetest.StartSession(t, independent, nil)
	enginetest.SessionSQL(t, other, "REFRESH MATERIALIZED VIEW gin_mv")
	ginCorePending(t, independent, ginCoreRelation(t, independent, "gin_mv_i"), 0, 3)
}

func ginCoreMaterializedUnique(t *testing.T, e *core.Engine, members int) {
	t.Helper()
	target := ginCoreRelation(t, e, "gin_mv_u")
	root, store := e.Storage.IndexRoots[target.Address()], e.Storage.Relations[target.SQLIndex.Parent]
	if root == nil || store == nil || len(root.Entries) != members || len(store.Versions) != members {
		t.Fatal("materialized unique match authority does not cover its actual heap", members)
	}
	seen := make(map[index.RowIdentity]bool, members)
	for _, entry := range root.Entries {
		if entry.Position < 0 || entry.Position >= len(store.Versions) {
			t.Fatal("materialized unique slot outside heap")
		}
		row := store.Versions[entry.Position]
		identity := index.RowIdentity{Row: row.Row, Version: row.Version}
		if row.Row != entry.Row || row.Version != entry.Version || seen[identity] || len(entry.Keys) != 1 || !sqltype.SameDatum(entry.Keys[0], row.Values[0]) {
			t.Fatal("materialized unique entry is not exact destination identity/key", entry)
		}
		seen[identity] = true
	}
}

func ginCoreMaterializedCleanRefresh(t *testing.T, e *core.Engine, explicit bool) {
	t.Helper()
	actor := enginetest.StartSession(t, e, nil)
	for _, name := range []string{"gin_mv_i", "gin_mv_e"} {
		enginetest.SessionSQL(t, actor, "SELECT gin_clean_pending_list('"+name+"'::regclass)")
		ginCorePending(t, e, ginCoreRelation(t, e, name), 0, 4)
	}
	refresh := func(want []int64) {
		t.Helper()
		type predecessor struct {
			target  *catalog.Relation
			root    *storage.IndexRoot
			payload *storage.GinRoot
		}
		var old []predecessor
		for _, name := range []string{"gin_mv_i", "gin_mv_e"} {
			target := ginCoreRelation(t, e, name)
			root := e.Storage.IndexRoots[target.Address()]
			old = append(old, predecessor{target, root, ginCoreCopyPayload(root.Gin)})
		}
		unique := ginCoreRelation(t, e, "gin_mv_u")
		uniqueRoot := e.Storage.IndexRoots[unique.Address()]
		uniqueEntries := slices.Clone(uniqueRoot.Entries)
		if explicit {
			enginetest.SessionSQL(t, actor, "BEGIN")
		}
		enginetest.SessionSQL(t, actor, "REFRESH MATERIALIZED VIEW CONCURRENTLY gin_mv")
		if explicit {
			func() {
				actor.Transactions.Mu.Lock()
				defer actor.Transactions.Mu.Unlock()
				e.Mu.Lock()
				defer e.Mu.Unlock()
				for _, prior := range old {
					candidate, ok := index.JournalIndexesOrZero(actor.Transactions.Catalog).Candidates[prior.target.Address()]
					if !ok || candidate.Root == nil || candidate.Root.Gin == nil || len(candidate.Root.Entries) != len(want) || storage.GinPendingIncidences(candidate.Root.Gin) != 0 || e.Storage.IndexRoots[prior.target.Address()] != prior.root {
						t.Fatal("private concurrent refresh lost cleaned source placement or published its candidate")
					}
				}
			}()
			enginetest.SessionSQL(t, actor, "COMMIT")
		}
		for _, prior := range old {
			ginCorePending(t, e, prior.target, 0, len(want))
			if !slices.Equal(ginCorePlacementIDs(t, e, prior.target, false), want) || len(ginCorePlacementIDs(t, e, prior.target, true)) != 0 {
				t.Fatal("concurrent refresh reintroduced pending from a cleaned restored source", prior.target.Name())
			}
			if !reflect.DeepEqual(prior.root.Gin, prior.payload) {
				t.Fatal("concurrent refresh mutated cleaned predecessor")
			}
		}
		if !reflect.DeepEqual(uniqueRoot.Entries, uniqueEntries) {
			t.Fatal("concurrent refresh mutated old unique match authority")
		}
		ginCoreMaterializedUnique(t, e, len(want))
	}
	refresh([]int64{1, 2, 3, 4})
	enginetest.SessionSQL(t, actor, "DELETE FROM gin_mv_base WHERE id=2")
	refresh([]int64{1, 3, 4})
}
