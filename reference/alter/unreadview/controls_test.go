package unreadview_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterTypeUnreadViewNativeControls(t *testing.T) {
	routes(t, func(s *scenario, mode string) {
		s.setup(true, true)
		held := s.prepare(s.conn, ids("v").Query)
		finish := s.begin(mode)
		s.replacement()
		finish(true)
		s.graph(1, 2)
		s.heldRead(held, ids("v", 1, 2))
		s.identity()
		s.links()
		if !s.b.PG {
			nativeGraphRoundTrip(s)
		}
	})
}

func nativeGraphRoundTrip(s *scenario) {
	s.t.Helper()
	retained, err := s.conn.QueryContext(s.p.Ctx, ids("v").Query)
	if err != nil {
		s.t.Fatal(err)
	}
	defer retained.Close()
	metadata := idMetadata(s.t, retained)
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	s.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('t','v','w','j','marker')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	restoredGraph(s)
	if got := idMetadata(s.t, retained); !reflect.DeepEqual(got, metadata) {
		s.t.Fatal("Reset/Restore mutated retained metadata", got, metadata)
	}
	consumeIDs(s.t, retained, 1, 2)
	// Cancellation and failed input preserve the already restored live target.
	before, old, definitions := s.identity(), s.oid("oldenum"), s.definitions()
	ctx, cancel := context.WithCancel(s.p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, s.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		s.t.Fatal("canceled Restore", err)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewBufferString("CREATE TABLE broken(")); err == nil {
		s.t.Fatal("invalid Restore succeeded")
	}
	s.sameIdentity(before)
	if s.oid("oldenum") != old || !reflect.DeepEqual(s.definitions(), definitions) {
		s.t.Fatal("failed Restore changed live catalog")
	}
	s.graph(1, 2, 3)
	s.links()
	// A fresh public Driver also has to import actual graph relationships,
	// independently of any coincidentally reused allocator numbers.
	connector, err := stuntgres.NewDriver().OpenConnector(s.t.Name() + "-restored")
	if err != nil {
		s.t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	if err := stuntgres.Restore(s.p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(s.t, connector)
	owner, peer := fixture.LockConnections(s.t, s.p.Ctx, recorded)
	fresh := *s
	fresh.conn, fresh.peer, fresh.e = owner, peer, owner
	fresh.b.DB, fresh.b.Recorder = recorded, recorder
	restoredGraph(&fresh)
}

func restoredGraph(s *scenario) {
	s.t.Helper()
	s.identity()
	s.links()
	s.graph(1, 2)
	s.run(fixture.Select("SELECT id,state::text FROM t ORDER BY id", []string{"id", "state"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "active"}, {int64(2), nil}}))
	handles := make(map[string]*sql.Stmt)
	for _, view := range []string{"v", "w", "j"} {
		handles[view] = s.prepare(s.conn, ids(view).Query)
		s.heldRead(handles[view], ids(view, 1, 2))
	}
	s.exec("INSERT INTO t VALUES(3,'dormant')", 1)
	s.exec("INSERT INTO marker VALUES(3)", 1)
	s.graph(1, 2, 3)
	for view, held := range handles {
		s.heldRead(held, ids(view, 1, 2, 3))
	}
	s.run(fixture.Select("SELECT state::text FROM t WHERE id=3", []string{"state"}, []string{"TEXT"}, [][]any{{"dormant"}}))
	s.failure("DROP TYPE oldenum", "2BP01", "cannot drop type oldenum because other objects depend on it", "column state of table t depends on type oldenum", dropHint)
	// Isolate one actual read edge so the expected DROP detail has one object,
	// rather than depending on allocator-dependent cascade traversal order.
	s.exec("DROP VIEW j", 0)
	s.exec("DROP VIEW w", 0)
	s.failure("DROP TABLE t RESTRICT", "2BP01", "cannot drop table t because other objects depend on it", "view v depends on table t", dropHint)
	s.failure("ALTER TABLE t DROP COLUMN id RESTRICT", "2BP01", "cannot drop column id of table t because other objects depend on it", "view v depends on column id of table t", dropHint)
	// Recreate the genuine consumers for subsequent target-preservation checks.
	s.exec("CREATE VIEW w AS SELECT id FROM v", 0)
	s.exec("CREATE VIEW j AS SELECT w.id FROM w JOIN marker ON w.id=marker.id", 0)
	s.graph(1, 2, 3)
	s.links()
}
