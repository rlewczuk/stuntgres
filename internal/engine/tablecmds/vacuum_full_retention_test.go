package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"testing"
)

func TestVacuumFullRetention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "update_history_commit", true: "update_history_prepared"}[prepared], func(t *testing.T) {
			e := engine.New()
			owner, reader, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE full_history(id INT PRIMARY KEY)", "INSERT INTO full_history VALUES(1)", "CREATE TABLE full_history_anchor(id INT)", "INSERT INTO full_history_anchor VALUES(9)"} {
				sessionSQL(t, owner, q)
			}
			sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			sessionSQL(t, reader, "SELECT id FROM full_history_anchor").Rows.Close()
			original := captureFullOwned(t, e, "full_history")
			for _, q := range []string{"BEGIN", "UPDATE full_history SET id=2", "SAVEPOINT saved_history", "UPDATE full_history SET id=99"} {
				sessionSQL(t, owner, q)
			}
			discardedVersion := owner.Transactions.Writes[original.ref.Address][0]
			discardedValue, err := discardedVersion.Values[0].Integer()
			if err != nil || discardedVersion.Born != 0 || discardedVersion.Version == original.rows[0].Version || discardedValue != 99 {
				t.Fatal("missing genuine accepted version to discard")
			}
			for _, q := range []string{"ROLLBACK TO saved_history", "UPDATE full_history SET id=3"} {
				sessionSQL(t, owner, q)
			}
			discarded := original.store.Discarded
			if discarded == nil || discarded.Next != nil || discarded.Version.Version != discardedVersion.Version || &discarded.Version.Values[0] != &discardedVersion.Values[0] {
				t.Fatal("savepoint did not retain exact discarded version/backing")
			}
			discardedOwned := make(map[*storage.HeapPredecessor]fullOwnedPredecessor)
			captureFullHistory(discarded, discardedOwned)
			writes := owner.Transactions.Writes[original.ref.Address]
			if len(writes) != 1 || writes[0].PrivateHistory == nil || writes[0].PrivateHistory.Next == nil || writes[0].PrivateHistory.Next.Next != nil {
				t.Fatal("missing actual private repeated UPDATE history")
			}
			private := writes[0].PrivateHistory
			privateOwned := make(map[*storage.HeapPredecessor]fullOwnedPredecessor)
			captureFullHistory(private, privateOwned)
			if prepared {
				sessionSQL(t, owner, "PREPARE TRANSACTION 'full_history'")
				entry := core.EnginePreparedRef(e).Lookup("full_history")
				if entry == nil || entry.Writes[original.ref.Address][0].PrivateHistory != private {
					t.Fatal("PREPARE did not transfer exact private history")
				}
				committed := captureFullOwned(t, e, "full_history")
				ctx, cancel := context.WithCancel(context.Background())
				op, err := worker.Acquire(ctx)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				cancel()
				_, err = e.Execute(op, enginetest.Statement(t, "COMMIT PREPARED 'full_history'"), 0, nil)
				op.Done()
				if !errors.Is(err, context.Canceled) || core.EnginePreparedRef(e).Lookup("full_history") != entry || entry.Writes[original.ref.Address][0].PrivateHistory != private {
					t.Fatal("canceled prepared completion lost actual private owner", err)
				}
				committed.rejected(t, e)
				sessionSQL(t, worker, "COMMIT PREPARED 'full_history'")
			} else {
				sessionSQL(t, owner, "COMMIT")
			}
			before := captureFullOwned(t, e, "full_history")
			if len(before.rows) != 1 || before.rows[0].PrivateHistory != nil || len(before.history) != 2 {
				t.Fatal("publication did not transfer two actual committed predecessors")
			}
			for node := before.predecessors; node != nil; node = node.Next {
				if node.End == 0 || node.EndFullXID < 3 || node.Version.Born == 0 || node.Version.PrivateHistory != nil {
					t.Fatal("invalid committed UPDATE predecessor", node)
				}
			}
			guarded, err := storage.RewriteVacuum(context.Background(), before.rows, before.ends, before.predecessors, e.Visibility.CommitSeq(), 3, false, nil)
			if err != nil || guarded.Live != 1 || guarded.Dead != 2 || guarded.Removed != 0 || guarded.Retained != before.predecessors || len(guarded.Versions) != 1 || guarded.Positions[0] != 0 {
				t.Fatal("guarded actual predecessor accounting/current-copy sizing", guarded, err)
			}
			read := catalog.NewRead(&e.Catalog.Owner, before.catalog)
			if report := table.VacuumFullReport(read, before.descriptor, guarded, 1); report != `rewriting "public"."full_history" (native logical storage): 1 live rows, 2 retained dead versions, 0 dead versions removed, 0 versions frozen, 1 indexes rebuilt` {
				t.Fatal("guarded physical history VERBOSE counts", report)
			}
			physical, err := storage.RewriteHeapVacuum(context.Background(), *before.store, e.Visibility.CommitSeq(), 3, false, nil)
			if err != nil || physical.Live != 1 || physical.Dead != 3 || physical.Removed != 0 || physical.Retained != before.predecessors || physical.Discarded != discarded || len(physical.Versions) != 1 {
				t.Fatal("guarded canonical rewrite lost committed or discarded physical versions", physical, err)
			}
			sessionSQL(t, worker, "VACUUM (FULL,VERBOSE) full_history")
			next := before.replaced(t, e)
			if next.Retained != before.predecessors || next.Discarded != discarded || len(next.Versions) != 1 || next.Ends.Len() != 0 || next.Maintenance.Tuples != 4 || next.Maintenance.Live != before.maintenance.Live || next.Maintenance.Dead != before.maintenance.Dead {
				t.Fatal("guarded FULL dropped actual history or miscounted physical versions", next.Maintenance)
			}
			result := sessionSQL(t, reader, "SELECT id FROM full_history")
			values := enginetest.Tuples(result)
			result.Rows.Close()
			if len(values) != 1 || values[0][0] != int64(1) {
				t.Fatal("old reader lost actual pre-UPDATE value", values)
			}
			sessionSQL(t, reader, "ROLLBACK")
			before = captureFullOwned(t, e, "full_history")
			retired, err := storage.RewriteVacuum(context.Background(), before.rows, before.ends, before.predecessors, e.Visibility.CommitSeq(), 3, true, nil)
			if err != nil || retired.Live != 1 || retired.Dead != 0 || retired.Removed != 2 || retired.Retained != nil || len(retired.Versions) != 1 || retired.Positions[0] != 0 {
				t.Fatal("quiescent actual predecessor accounting/current-copy sizing", retired, err)
			}
			if report := table.VacuumFullReport(read, before.descriptor, retired, 1); report != `rewriting "public"."full_history" (native logical storage): 1 live rows, 0 retained dead versions, 2 dead versions removed, 0 versions frozen, 1 indexes rebuilt` {
				t.Fatal("retired physical history VERBOSE counts", report)
			}
			physical, err = storage.RewriteHeapVacuum(context.Background(), *before.store, e.Visibility.CommitSeq(), 3, true, nil)
			if err != nil || physical.Live != 1 || physical.Dead != 0 || physical.Removed != 3 || physical.Retained != nil || physical.Discarded != nil || len(physical.Versions) != 1 {
				t.Fatal("quiescent canonical rewrite did not retire exact committed and discarded versions", physical, err)
			}
			sessionSQL(t, worker, "VACUUM (FULL,VERBOSE) full_history")
			next = before.replaced(t, e)
			if next.Retained != nil || next.Discarded != nil || len(next.Versions) != 1 || next.Maintenance.Tuples != 1 || next.Maintenance.Live != before.maintenance.Live || next.Maintenance.Dead != before.maintenance.Dead || next.Maintenance.Updates != before.maintenance.Updates || next.Maintenance.Modifications != before.maintenance.Modifications || next.Maintenance.VacuumCount != before.maintenance.VacuumCount {
				t.Fatal("quiescent FULL did not retire exact history while preserving cumulative facts", next.Maintenance)
			}
			for node, owned := range privateOwned {
				if node.Next != owned.header.Next || node.Version.PrivateHistory != owned.header.Version.PrivateHistory || !reflect.DeepEqual(*node, owned.header) || len(owned.payload) != 0 && &node.Version.Values[0] != &owned.payload[0] {
					t.Fatal("FULL/publication mutated saved private history")
				}
			}
			for node, owned := range discardedOwned {
				if !reflect.DeepEqual(*node, owned.header) || &node.Version.Values[0] != &owned.payload[0] || node.Version.Version != discardedVersion.Version {
					t.Fatal("FULL mutated captured discarded version/header/backing")
				}
			}
			fullMixedRows(t, worker, "full_history", 3)
			lockEmpty(t, e)
		})
	}
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing_ticket", true: "late_ticket"}[late], func(t *testing.T) {
			e := engine.New()
			owner, reader, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE full_keep(id INT)", "INSERT INTO full_keep VALUES(1),(2)", "CREATE TABLE full_anchor(id INT)", "INSERT INTO full_anchor VALUES(9)"} {
				sessionSQL(t, owner, q)
			}
			pin := func() {
				sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				result := sessionSQL(t, reader, "SELECT id FROM full_anchor")
				result.Rows.Close()
			}
			if !late {
				pin()
			}
			sessionSQL(t, owner, "DELETE FROM full_keep WHERE id=1")
			before := captureFullOwned(t, e, "full_keep")
			if late {
				task := startVacuumBarrier(t, e, worker, "VACUUM FULL full_keep", "storage.RewriteVacuum")
				pin()
				task.join(t, nil)
			} else {
				sessionSQL(t, worker, "VACUUM FULL full_keep")
			}
			result := sessionSQL(t, reader, "SELECT id FROM full_keep ORDER BY id")
			rows := enginetest.Tuples(result)
			result.Rows.Close()
			if late {
				if len(rows) != 1 || rows[0][0] != int64(2) {
					t.Fatal("late snapshot selected wrong rewrite root", rows)
				}
			} else if len(rows) != 2 || rows[0][0] != int64(1) || rows[1][0] != int64(2) {
				t.Fatal("retained snapshot lost pre-FULL visibility", rows)
			}
			next := before.replaced(t, e)
			if len(next.Versions) != 2 || next.Ends.Len() != 1 {
				t.Fatal("unrelated live ticket lost protected ended head")
			}
			sessionSQL(t, reader, "ROLLBACK")
			before = captureFullOwned(t, e, "full_keep")
			sessionSQL(t, worker, "VACUUM FULL full_keep")
			next = before.replaced(t, e)
			if len(next.Versions) != 1 || next.Ends.Len() != 0 || next.Versions[0].Row != before.rows[1].Row {
				t.Fatal("quiescent FULL did not retire exact obsolete head")
			}
			lockEmpty(t, e)
		})
	}
	t.Run("repeated_full_readers_and_dml", func(t *testing.T) {
		e := engine.New()
		owner, first, second := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE full_repeat(id INT PRIMARY KEY)", "INSERT INTO full_repeat VALUES(1),(2)", "CREATE TABLE full_repeat_anchor(id INT)", "INSERT INTO full_repeat_anchor VALUES(9)"} {
			sessionSQL(t, owner, q)
		}
		sessionSQL(t, first, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		sessionSQL(t, first, "SELECT id FROM full_repeat_anchor").Rows.Close()
		sessionSQL(t, owner, "DELETE FROM full_repeat WHERE id=1")
		before := captureFullOwned(t, e, "full_repeat")
		sessionSQL(t, owner, "VACUUM FULL full_repeat")
		before.replaced(t, e)
		sessionSQL(t, second, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		sessionSQL(t, second, "SELECT id FROM full_repeat_anchor").Rows.Close()
		sessionSQL(t, owner, "UPDATE full_repeat SET id=3 WHERE id=2")
		sessionSQL(t, owner, "INSERT INTO full_repeat VALUES(4)")
		afterDML := captureFullOwned(t, e, "full_repeat")
		sessionSQL(t, owner, "VACUUM FULL full_repeat")
		afterDML.replaced(t, e)
		before.retained(t)
		for i, reader := range []*session.Session{first, second, owner} {
			result := sessionSQL(t, reader, "SELECT id FROM full_repeat ORDER BY id")
			rows := enginetest.Tuples(result)
			result.Rows.Close()
			want := [][]int64{{1, 2}, {2}, {3, 4}}[i]
			if len(rows) != len(want) {
				t.Fatal("repeat FULL snapshot rows", i, rows)
			}
			for j, id := range want {
				if rows[j][0] != id {
					t.Fatal("repeat FULL snapshot value", i, rows)
				}
			}
		}
		sessionSQL(t, first, "ROLLBACK")
		sessionSQL(t, second, "ROLLBACK")
		before = captureFullOwned(t, e, "full_repeat")
		sessionSQL(t, owner, "VACUUM FULL full_repeat")
		if next := before.replaced(t, e); len(next.Versions) != 2 || next.Ends.Len() != 0 {
			t.Fatal("quiescent repeated FULL retained obsolete heads")
		}
		lockEmpty(t, e)
	})
	for _, wrap := range []bool{false, true} {
		t.Run(map[bool]string{false: "publisher", true: "publisher_wrap"}[wrap], func(t *testing.T) {
			e := engine.New()
			older, owner := sessionStart(t, e, nil), sessionStart(t, e, nil)
			if wrap {
				e.Mu.Lock()
				e.Visibility.SetLastAssignedXIDLocked(0xfffffffe)
				e.Mu.Unlock()
			}
			sessionSQL(t, older, "BEGIN")
			assignedResult := sessionSQL(t, older, "SELECT pg_current_xact_id()")
			assignedResult.Rows.Close()
			e.Mu.Lock()
			assigned := e.Visibility.FullXIDLocked(older.Transactions.Ref)
			e.Mu.Unlock()
			sessionSQL(t, owner, "CREATE TABLE full_horizon(id INT)")
			sessionSQL(t, owner, "INSERT INTO full_horizon VALUES(1)")
			_, old := vacuumStore(t, e, "full_horizon")
			identity := old.Versions[0].InsertFullXID
			sessionSQL(t, owner, "VACUUM (FULL,FREEZE) full_horizon")
			_, next := vacuumStore(t, e, "full_horizon")
			if next == old || next.Maintenance.FrozenXID > assigned || next.Versions[0].InsertFullXID != identity || next.Versions[0].Frozen {
				t.Fatal("rewrite invented freeze provenance or passed assigned publisher")
			}
			sessionSQL(t, older, "ROLLBACK")
			sessionSQL(t, owner, "VACUUM FULL full_horizon")
			_, final := vacuumStore(t, e, "full_horizon")
			if !final.Versions[0].Frozen || final.Versions[0].InsertFullXID != identity {
				t.Fatal("eligible freeze lost insertion identity")
			}
		})
	}
}

func TestVacuumFullIndexOwnership(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE ROLE full_recipe_owner", "CREATE ROLE full_recipe_actor", "CREATE TABLE full_owner_heap(id INT)", "INSERT INTO full_owner_heap VALUES(1),(2)", "CREATE FUNCTION full_owner_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN IF current_user <> 'full_recipe_owner' THEN RAISE EXCEPTION 'wrong owner'; END IF; RETURN i; END $$", "ALTER FUNCTION full_owner_key(INT) OWNER TO full_recipe_owner", "ALTER TABLE full_owner_heap OWNER TO full_recipe_owner", "GRANT USAGE ON SCHEMA public TO full_recipe_owner,full_recipe_actor", "GRANT CREATE ON SCHEMA public TO full_recipe_owner", "SET ROLE full_recipe_owner", "CREATE INDEX full_owner_ix ON full_owner_heap(full_owner_key(id))", "RESET ROLE", "REVOKE EXECUTE ON FUNCTION full_owner_key(INT) FROM PUBLIC", "GRANT MAINTAIN ON full_owner_heap TO full_recipe_actor", "SET ROLE full_recipe_actor", "SET search_path=public,pg_catalog"} {
		sessionSQL(t, s, q)
	}
	before := captureFullOwned(t, e, "full_owner_heap")
	sessionSQL(t, s, "VACUUM FULL full_owner_heap")
	before.replaced(t, e)
	// Operation-scoped restricted settings/identity must not leak to the session.
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if op.Restricted != nil {
		t.Fatal("owner environment leaked")
	}
	if path, _ := op.Setting("search_path"); path != "public, pg_catalog" && path != "public,pg_catalog" {
		t.Fatal("safe path escaped operation", path)
	}
	op.Done()
	// A failing follow-on ANALYZE cannot roll back the already published FULL.
	sessionSQL(t, s, "RESET ROLE")
	before = captureFullOwned(t, e, "full_owner_heap")
	op, err = s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "VACUUM (FULL,ANALYZE) full_owner_heap(missing)"), 0, nil)
	op.Done()
	if err == nil {
		t.Fatal("missing analysis column accepted")
	}
	before.replaced(t, e)
	sessionSQL(t, s, "CREATE OR REPLACE FUNCTION full_owner_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN RAISE EXCEPTION 'rebuild rejected'; END $$")
	before = captureFullOwned(t, e, "full_owner_heap")
	op, err = s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "VACUUM FULL full_owner_heap"), 0, nil)
	if op.Restricted != nil {
		t.Fatal("failing recipe leaked restricted invocation")
	}
	op.Done()
	enginetest.State(t, err, "P0001")
	if !strings.Contains(err.Error(), "rebuild rejected") {
		t.Fatal("replacement body did not produce the rebuild failure", err)
	}
	before.rejected(t, e)
}
