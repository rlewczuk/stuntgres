package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"testing"
	"testing/synctest"
	"time"
)

// All ten-second intervals below are virtual synctest time, never wall sleeps.
func TestStatsSnapshotIdlePublicationAuthority(t *testing.T) {
	for _, mode := range []string{"deadline", "transaction", "aborted", "activity", "admission", "owner", "rows", "message", "writers", "replacement", "end_incarnation", "terminated", "abandoned", "detached", "initialization_failure"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 12345})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { statsEnd(t, s) })
				enginetest.SessionSQL(t, s, "CREATE TABLE stats_idle(id integer)")
				key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_idle")}
				want := func(inserts int64) {
					t.Helper()
					if f := e.Statistics.Read(key); f.Inserts != inserts {
						t.Fatalf("%s inserts=%d want=%d: %+v", mode, f.Inserts, inserts, f)
					}
				}
				enginetest.SessionSQL(t, s, "INSERT INTO stats_idle VALUES(1)")
				want(1)
				if mode == "deadline" {
					time.Sleep(999 * time.Millisecond)
				}
				enginetest.SessionSQL(t, s, "INSERT INTO stats_idle VALUES(2)")
				want(1)
				switch mode {
				case "deadline":
					time.Sleep(500 * time.Microsecond)
					enginetest.SessionSQL(t, s, "SELECT 1")
					want(1)
					time.Sleep(10*time.Second - 500*time.Microsecond)
					synctest.Wait()
					want(2)
					time.Sleep(10 * time.Second)
					synctest.Wait()
					want(2)
				case "transaction", "aborted":
					enginetest.SessionSQL(t, s, "BEGIN")
					enginetest.SessionSQL(t, s, "INSERT INTO stats_idle VALUES(3)")
					enginetest.SessionSQL(t, s, "SAVEPOINT child")
					enginetest.SessionSQL(t, s, "INSERT INTO stats_idle VALUES(4)")
					enginetest.SessionSQL(t, s, "ROLLBACK TO child")
					want(1)
					if mode == "aborted" {
						op, err := s.AcquireActivity(context.Background())
						if err != nil {
							t.Fatal(err)
						}
						scope, err := e.StartStatement(op)
						if err != nil {
							t.Fatal(err)
						}
						_, err = scope.Execute(enginetest.Statement(t, "SELECT missing_idle_column FROM stats_idle"), 0, nil)
						if err == nil {
							t.Fatal("missing column unexpectedly accepted")
						}
						if err = scope.Finish(err); err == nil {
							t.Fatal("statement error lost")
						}
						op.Done()
					}
					time.Sleep(time.Second)
					synctest.Wait()
					want(1)
					if mode == "transaction" {
						enginetest.SessionSQL(t, s, "SELECT 1")
						want(1)
					}
					time.Sleep(9 * time.Second)
					synctest.Wait()
					want(1)
					enginetest.SessionSQL(t, s, "ROLLBACK")
					want(4)
					f := e.Statistics.Read(key)
					if f.Live != 2 || f.Dead != 2 || f.Modifications != 2 || f.InsertsSince != 4 {
						t.Fatal("deferred transaction settlement", f)
					}
				case "activity":
					op, err := s.AcquireActivity(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					time.Sleep(10 * time.Second)
					synctest.Wait()
					want(1)
					op.Done()
					want(2)
				case "admission", "owner":
					if mode == "admission" {
						s.Transactions.Admission.Lock()
					} else {
						s.Transactions.Mu.Lock()
					}
					time.Sleep(10 * time.Second)
					synctest.Wait()
					want(1)
					if mode == "admission" {
						s.Transactions.Admission.Unlock()
					} else {
						s.Transactions.Mu.Unlock()
					}
					op, err := s.AcquireActivity(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					op.Done()
					want(2)
				case "rows", "message":
					op, err := s.AcquireActivity(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "message" {
						if err = scope.RetainMessage(); err != nil {
							t.Fatal(err)
						}
					}
					result, err := scope.Execute(enginetest.Statement(t, "SELECT id FROM stats_idle"), 0, nil)
					if err != nil || result.Rows == nil {
						t.Fatal("retained source", err)
					}
					if mode == "message" {
						if err = result.Rows.Close(); err != nil {
							t.Fatal(err)
						}
						if err = scope.Finish(nil); err != nil {
							t.Fatal(err)
						}
					}
					time.Sleep(10 * time.Second)
					synctest.Wait()
					want(1)
					if mode == "message" {
						err = scope.FinishMessage(nil)
					} else {
						err = result.Rows.Close()
						if err == nil {
							err = scope.Finish(nil)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					op.Done()
					want(2)
				case "initialization_failure":
					failure := errors.New("idle publication startup failure")
					failed, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 54321, Defaults: &defaultProvider{err: failure}})
					if failed != nil || !errors.Is(err, failure) {
						t.Fatal("startup failure did not drain", failed, err)
					}
					reused, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 54321})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { statsEnd(t, reused) })
					enginetest.SessionSQL(t, reused, "INSERT INTO stats_idle VALUES(3)")
					want(2)
					time.Sleep(10 * time.Second)
					synctest.Wait()
					want(3)
				case "detached":
					s.Mu.Lock()
					handle := s.Resources[session.Transaction].Handle
					s.Mu.Unlock()
					if err = s.Detach(handle, session.CleanClose); err != nil {
						t.Fatal(err)
					}
					time.AfterFunc(10*time.Second, func() { s.RequestEnd(session.CleanClose) })
					time.Sleep(10 * time.Second)
					if err = s.WaitEnd(context.Background()); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					want(2)
				case "writers":
					time.Sleep(5 * time.Second)
					other, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 54321})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { statsEnd(t, other) })
					enginetest.SessionSQL(t, other, "INSERT INTO stats_idle VALUES(3)")
					want(2)
					enginetest.SessionSQL(t, other, "INSERT INTO stats_idle VALUES(4)")
					want(2)
					time.Sleep(5 * time.Second)
					synctest.Wait()
					want(3)
					time.Sleep(5 * time.Second)
					synctest.Wait()
					want(4)
				case "replacement":
					source := engine.New()
					sourceSession := enginetest.StartSession(t, source, nil)
					enginetest.SessionSQL(t, sourceSession, "CREATE TABLE stats_idle(id integer)")
					enginetest.SessionSQL(t, sourceSession, "INSERT INTO stats_idle VALUES(9)")
					if _, err = e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
						t.Fatal(err)
					}
					replacementKey := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_idle")}
					time.Sleep(10 * time.Second)
					synctest.Wait()
					if f := e.Statistics.Read(replacementKey); f.Inserts != 0 || f.Live != 1 {
						t.Fatal("old timer crossed replacement identity", f)
					}
					if f := e.Statistics.Read(key); f.Inserts != 0 {
						t.Fatal("stale batch reappeared after replacement", f)
					}
				case "end_incarnation", "terminated", "abandoned":
					// Both real callbacks become runnable at the same virtual deadline.
					// Either ordering must join safely and settle the pending batch once.
					reason := session.CleanClose
					if mode == "terminated" {
						reason = session.Terminated
					}
					if mode == "abandoned" {
						reason = session.Abandoned
					}
					time.AfterFunc(10*time.Second, func() { s.RequestEnd(reason) })
					time.Sleep(10 * time.Second)
					if err = s.WaitEnd(context.Background()); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					want(2)
					replacement, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 12345})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { statsEnd(t, replacement) })
					enginetest.SessionSQL(t, replacement, "INSERT INTO stats_idle VALUES(3)")
					want(3)
					enginetest.SessionSQL(t, replacement, "INSERT INTO stats_idle VALUES(4)")
					want(3)
					time.Sleep(9 * time.Second)
					synctest.Wait()
					want(3)
					time.Sleep(time.Second)
					synctest.Wait()
					want(4)
				}
			})
		})
	}
	t.Run("normal_drain_cancels_old_callback", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE stats_cancel(id integer)")
			key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_cancel")}
			enginetest.SessionSQL(t, s, "INSERT INTO stats_cancel VALUES(1)")
			enginetest.SessionSQL(t, s, "INSERT INTO stats_cancel VALUES(2)")
			time.Sleep(time.Second)
			enginetest.SessionSQL(t, s, "SELECT 1")
			if e.Statistics.Read(key).Inserts != 2 {
				t.Fatal("ordinary due flush failed")
			}
			time.Sleep(8500 * time.Millisecond)
			enginetest.SessionSQL(t, s, "INSERT INTO stats_cancel VALUES(3)")
			enginetest.SessionSQL(t, s, "INSERT INTO stats_cancel VALUES(4)")
			time.Sleep(500 * time.Millisecond)
			synctest.Wait()
			if e.Statistics.Read(key).Inserts != 3 {
				t.Fatal("obsolete ten-second callback flushed newer pending")
			}
			time.Sleep(9500 * time.Millisecond)
			synctest.Wait()
			if e.Statistics.Read(key).Inserts != 4 {
				t.Fatal("new one-shot timeout failed")
			}
		})
	})
}
