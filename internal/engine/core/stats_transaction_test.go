package core_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"testing"
)

func statsEnd(t *testing.T, s *session.Session) {
	t.Helper()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestStatsSnapshotLedgerOwnership(t *testing.T) {
	t.Run("terminal_abort", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_terminal(id integer)")
		ref := enginetest.RoleTableRef(t, e, "stats_terminal")
		enginetest.SessionSQL(t, s, "BEGIN")
		enginetest.SessionSQL(t, s, "INSERT INTO stats_terminal VALUES (1),(2)")
		statsEnd(t, s)
		if f := e.Statistics.Read(statistics.Key{Object: ref}); f.Inserts != 2 || f.Live != 0 || f.Dead != 2 || f.InsertsSince != 2 {
			t.Fatal("terminal drain published private live rows", f)
		}
	})
	t.Run("abort_savepoint_attempts", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_abort(id integer)")
		ref := enginetest.RoleTableRef(t, e, "stats_abort")
		key := statistics.Key{Object: ref}
		for _, q := range []string{"BEGIN", "INSERT INTO stats_abort VALUES (1),(2),(3)", "SAVEPOINT a", "INSERT INTO stats_abort VALUES (4),(5)", "ROLLBACK TO a", "INSERT INTO stats_abort VALUES (6)", "ROLLBACK TO a"} {
			enginetest.SessionSQL(t, s, q)
		}
		if s.Transactions.Catalog != nil || s.Transactions.Savepoints[0].Stats[key].Inserts != 3 {
			t.Fatal("statistics created catalog ownership or mutated a retained mark")
		}
		f := s.Transactions.Stats[key]
		if f.Inserts != 3 || f.Live != 3 {
			t.Fatal("savepoint shared discarded mutable ledger", f)
		}
		// Even completed aborted children stay local while the parent is open.
		if p := e.Statistics.Read(key); p.Live != 0 || p.Inserts != 0 || p.Dead != 0 || p.InsertsSince != 0 {
			t.Fatal("transaction idle published cumulative tuple counters", p)
		}
		enginetest.SessionSQL(t, s, "ROLLBACK")
		statsEnd(t, s)
		if f := e.Statistics.Read(key); f.Inserts != 6 || f.Live != 0 || f.Dead != 6 || f.Modifications != 0 || f.InsertsSince != 6 {
			t.Fatal("abort erased attempts or published live rows", f)
		}
	})
	t.Run("commit_operation_policy", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_policy(id integer)")
		ref := enginetest.RoleTableRef(t, e, "stats_policy")
		for _, q := range []string{"BEGIN", "INSERT INTO stats_policy VALUES (1),(2)", "SET track_counts=off", "INSERT INTO stats_policy VALUES (3)", "SET track_counts=on", "UPDATE stats_policy SET id=11 WHERE id=1", "DELETE FROM stats_policy WHERE id=2", "SET track_counts=off", "COMMIT"} {
			enginetest.SessionSQL(t, s, q)
		}
		statsEnd(t, s)
		f := e.Statistics.Read(statistics.Key{Object: ref})
		if f.Inserts != 2 || f.Updates != 1 || f.Deletes != 1 || f.Live != 1 || f.Dead != 2 || f.Modifications != 4 {
			t.Fatal("COMMIT used final policy or physical counters", f)
		}
	})
	t.Run("truncate_commit", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_truncate(id integer)")
		ref := enginetest.RoleTableRef(t, e, "stats_truncate")
		for _, q := range []string{"BEGIN", "INSERT INTO stats_truncate VALUES (1),(2)", "TRUNCATE stats_truncate", "INSERT INTO stats_truncate VALUES (3)", "COMMIT"} {
			enginetest.SessionSQL(t, s, q)
		}
		statsEnd(t, s)
		f := e.Statistics.Read(statistics.Key{Object: ref})
		if f.Inserts != 1 || f.Live != 1 || f.Dead != 0 || f.Modifications != 1 || f.InsertsSince != 1 {
			t.Fatal("truncate accounting", f)
		}
	})
	for _, commit := range []bool{false, true} {
		name := "prepared_abort"
		if commit {
			name = "prepared_commit"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			finisher := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE stats_prepared(id integer)")
			ref := enginetest.RoleTableRef(t, e, "stats_prepared")
			key := statistics.Key{Object: ref}
			for _, q := range []string{"BEGIN", "INSERT INTO stats_prepared VALUES (1),(2)", "PREPARE TRANSACTION 'stats_pending'"} {
				enginetest.SessionSQL(t, s, q)
			}
			if s.Transactions.Catalog != nil || s.Transactions.Stats != nil {
				t.Fatal("PREPARE retained transaction ledger on physical session")
			}
			entry := core.EnginePreparedRef(e).Lookup("stats_pending")
			if entry == nil || entry.Stats[key].Live != 2 {
				t.Fatal("PREPARE lost ledger ownership")
			}
			statsEnd(t, s)
			if f := e.Statistics.Read(key); f.Inserts != 0 || f.Live != 0 || f.Dead != 0 || f.InsertsSince != 0 {
				t.Fatal("preparing exit published unresolved tuple counters", f)
			}
			terminal := "ROLLBACK PREPARED 'stats_pending'"
			if commit {
				terminal = "COMMIT PREPARED 'stats_pending'"
			}
			enginetest.SessionSQL(t, finisher, terminal)
			statsEnd(t, finisher)
			if entry.Stats != nil {
				t.Fatal("prepared settlement retained consumable ledger")
			}
			f := e.Statistics.Read(key)
			wantLive, wantDead := int64(0), int64(2)
			if commit {
				wantLive, wantDead = 2, 0
			}
			if f.Inserts != 2 || f.Live != wantLive || f.Dead != wantDead || f.InsertsSince != 2 {
				t.Fatal("prepared completion", commit, f)
			}
		})
	}
	t.Run("exception_suffix", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_exception(id integer)")
		ref := enginetest.RoleTableRef(t, e, "stats_exception")
		enginetest.SessionSQL(t, s, "DO $$BEGIN BEGIN INSERT INTO stats_exception VALUES (1); RAISE EXCEPTION 'rollback'; EXCEPTION WHEN others THEN PERFORM 1; END; INSERT INTO stats_exception VALUES (2); END$$")
		statsEnd(t, s)
		f := e.Statistics.Read(statistics.Key{Object: ref})
		if f.Inserts != 2 || f.Live != 1 || f.Dead != 1 || f.InsertsSince != 2 {
			t.Fatal("exception suffix lost attempted/dead accounting", f)
		}
	})
}
