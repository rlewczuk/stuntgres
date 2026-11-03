package core_test

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func TestGINPendingListLimitRootPreservation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gin_limit_roots(id integer,a integer[])")
	enginetest.Run(t, e, "INSERT INTO gin_limit_roots VALUES(1,'{1,2,10000}'),(2,'{}'),(3,NULL)")
	enginetest.Run(t, e, "CREATE INDEX gin_limit_roots_i ON gin_limit_roots USING gin(a) WITH(fastupdate=on)")
	descriptor, root := btreeRoot(t, e, "gin_limit_roots_i")
	before := ginAmcheckClone(root)
	parent := e.Catalog.State.MustRelation(descriptor.SQLIndex.Parent)
	store := e.Storage.Relations[parent.Address()]
	columnBacking := &root.Gin.Columns[0]
	tokenBacking := &root.Gin.Columns[0].Tokens[0]
	postingBacking := &root.Gin.Columns[0].Tokens[0].Postings[0]
	s := enginetest.StartSession(t, e, nil)
	check := func(display, raw, source string) {
		t.Helper()
		actual := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT current_setting('gin_pending_list_limit'),setting,source FROM pg_settings WHERE name='gin_pending_list_limit'"))
		if !reflect.DeepEqual(actual, [][]driver.Value{{display, raw, source}}) {
			t.Fatal("effective journal value", actual, display, raw, source)
		}
		current, currentRoot := btreeRoot(t, e, "gin_limit_roots_i")
		if current != descriptor || currentRoot != root || e.Storage.Relations[parent.Address()] != store || !reflect.DeepEqual(root, before) || &root.Gin.Columns[0] != columnBacking || &root.Gin.Columns[0].Tokens[0] != tokenBacking || &root.Gin.Columns[0].Tokens[0].Postings[0] != postingBacking {
			t.Fatal("setting/read/empty cleanup replaced or mutated logical GIN authority/backing")
		}
		bindings, err := index.BindGinIndex(e.CatalogRead(), parent, descriptor.SQLIndex)
		if err != nil || len(bindings) != len(root.Gin.Columns) {
			t.Fatal("current GIN bindings", bindings, err)
		}
		for i, binding := range bindings {
			if !root.Gin.Columns[i].Binding.Same(binding) {
				t.Fatal("saved binding lost current authority", i)
			}
		}
		if len(s.Transactions.Writes) != 0 || len(s.Transactions.Deletes) != 0 || (s.Transactions.Catalog != nil && len(index.JournalIndexesOrZero(s.Transactions.Catalog).Candidates) != 0) {
			t.Fatal("setting/read/empty cleanup staged data or index roots")
		}
		rows := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT id,a::text FROM gin_limit_roots ORDER BY id"))
		if !reflect.DeepEqual(rows, [][]driver.Value{{int64(1), "{1,2,10000}"}, {int64(2), "{}"}, {int64(3), nil}}) {
			t.Fatal("logical arrays changed", rows)
		}
		ids := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT id FROM gin_limit_roots WHERE a @> '{1,10000}'::integer[] ORDER BY id"))
		if !reflect.DeepEqual(ids, [][]driver.Value{{int64(1)}}) {
			t.Fatal("logical postings changed", ids)
		}
	}
	check("4MB", "4096", "default")
	enginetest.SessionSQL(t, s, "SET gin_pending_list_limit='8MB'")
	check("8MB", "8192", "session")
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SAVEPOINT before_local")
	enginetest.SessionSQL(t, s, "SET LOCAL gin_pending_list_limit='64kB'")
	check("64kB", "64", "session")
	enginetest.SessionSQL(t, s, "SAVEPOINT before_cleanup")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT gin_clean_pending_list('gin_limit_roots_i'::regclass)")); !reflect.DeepEqual(got, [][]driver.Value{{int64(0)}}) {
		t.Fatal("bulk-built index has no pending work", got)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO before_cleanup")
	check("64kB", "64", "session")
	enginetest.SessionSQL(t, s, "ROLLBACK TO before_local")
	check("8MB", "8192", "session")
	enginetest.SessionSQL(t, s, "ROLLBACK")
	check("8MB", "8192", "session")
	enginetest.SessionSQL(t, s, "RESET gin_pending_list_limit")
	check("4MB", "4096", "default")
	// A GIN created in the current uncommitted catalog is admitted too.
	// Its genuine bulk build has no pending work.
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "CREATE INDEX gin_limit_private_i ON gin_limit_roots USING gin(a)")
	enginetest.SessionSQL(t, s, "SAVEPOINT private_cleanup")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT gin_clean_pending_list('gin_limit_private_i'::regclass)")); !reflect.DeepEqual(got, [][]driver.Value{{int64(0)}}) {
		t.Fatal("private bulk-built index has no pending work", got)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO private_cleanup")
	enginetest.SessionSQL(t, s, "ROLLBACK")
	check("4MB", "4096", "default")
}

func TestGINPendingListLimitPendingPreservation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gin_limit_pending(id integer,a integer[])")
	enginetest.Run(t, e, "CREATE INDEX gin_limit_pending_i ON gin_limit_pending USING gin(a)")
	enginetest.Run(t, e, "INSERT INTO gin_limit_pending VALUES(1,'{1,2}'),(2,'{}'),(3,NULL)")
	descriptor, root := btreeRoot(t, e, "gin_limit_pending_i")
	if storage.GinPendingIncidences(root.Gin) == 0 || len(root.Gin.Columns[0].Tokens) != 0 {
		t.Fatal("fixture lacks genuine deferred postings")
	}
	before := ginAmcheckClone(root)
	store := e.Storage.Relations[descriptor.SQLIndex.Parent]
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"SET gin_pending_list_limit='8MB'",
		"BEGIN",
		"SAVEPOINT gin_limit_pending_mark",
		"SET LOCAL gin_pending_list_limit='64kB'",
		"SELECT set_config('gin_pending_list_limit','128kB',true)",
	} {
		enginetest.SessionSQL(t, s, query)
		current, next := btreeRoot(t, e, "gin_limit_pending_i")
		if current != descriptor || next != root || !reflect.DeepEqual(root, before) || e.Storage.Relations[descriptor.SQLIndex.Parent] != store {
			t.Fatal("setting assignment drained or replaced pending authority", query)
		}
	}
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT gin_clean_pending_list('gin_limit_pending_i')>0")); !reflect.DeepEqual(got, [][]driver.Value{{true}}) {
		t.Fatal("explicit cleanup did not transfer real pending work", got)
	}
	_, cleaned := btreeRoot(t, e, "gin_limit_pending_i")
	if cleaned == root || storage.GinPendingIncidences(cleaned.Gin) != 0 || len(cleaned.Gin.Columns[0].Tokens) == 0 || !reflect.DeepEqual(cleaned.Entries, root.Entries) || !reflect.DeepEqual(root, before) {
		t.Fatal("explicit cleanup lost membership or mutated its predecessor")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO gin_limit_pending_mark")
	enginetest.SessionSQL(t, s, "ROLLBACK")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT gin_clean_pending_list('gin_limit_pending_i')")); !reflect.DeepEqual(got, [][]driver.Value{{int64(0)}}) {
		t.Fatal("setting rollback resurrected pending work", got)
	}
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT current_setting('gin_pending_list_limit'),id FROM gin_limit_pending WHERE a @> '{2}'::integer[]")); !reflect.DeepEqual(got, [][]driver.Value{{"8MB", int64(1)}}) {
		t.Fatal("cleanup changed logical matches or setting rollback", got)
	}
}
