package core_test

import (
	"bytes"
	"context"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func statsSequenceCount(t *testing.T, e *core.Engine, key statistics.Key, want int64) {
	t.Helper()
	if n := e.Statistics.Read(key).SequencePageAccesses; n != want {
		t.Fatalf("sequence key %+v encounters=%d want=%d", key, n, want)
	}
}

func TestStatsSequenceRuntimeLifecycle(t *testing.T) {
	for _, refill := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending_fenced", true: "next_real_refill"}[refill], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, s, "CREATE SEQUENCE stats_sequence_reset CACHE 3")
				// Admission/setup used the first shared-writer publication.
				time.Sleep(time.Second)
				statsFunctionExec(t, s, "BEGIN", "SELECT nextval('stats_sequence_reset')", "COMMIT")
				key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_sequence_reset")}
				statsSequenceCount(t, e, key, 1)
				statsFunctionExec(t, s, "BEGIN", "SELECT nextval('stats_sequence_reset')", "SELECT nextval('stats_sequence_reset')", "SELECT nextval('stats_sequence_reset')")
				statsSequenceCount(t, e, key, 1) // Second reservation is still pending.
				e.Statistics.Reset(time.Now())
				statsFunctionExec(t, s, "SELECT nextval('stats_sequence_reset')", "SELECT nextval('stats_sequence_reset')")
				statsSequenceCount(t, e, key, 0) // Reset does not discard the warm value tail.
				if refill {
					statsFunctionExec(t, s, "SELECT nextval('stats_sequence_reset')")
				}
				statsFunctionExec(t, s, "ROLLBACK")
				statsFunctionRetire(t, s)
				want := int64(0)
				if refill {
					want = 1
				}
				statsSequenceCount(t, e, key, want)
			})
		})
	}
	for _, control := range []string{"reset", "restore"} {
		t.Run("surviving_temp/"+control, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, s, "CREATE SEQUENCE stats_sequence_persistent", "CREATE TEMP SEQUENCE stats_sequence_temp CACHE 3")
				time.Sleep(time.Second)
				statsFunctionExec(t, s, "BEGIN", "SELECT nextval('stats_sequence_temp')", "COMMIT")
				// pg_temp is a physical-session alias; the bootstrap-only
				// RoleTableRef helper has no authority for this owner's alias.
				read := e.CatalogRead()
				address, ok := read.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: "stats_sequence_temp"})
				if !ok || read.MustNamespace(s.TemporaryNamespace).Temporary != s.SessionToken {
					t.Fatal("missing owner TEMP sequence")
				}
				old := statistics.Key{Object: e.Catalog.Domain.Ref(read.Epoch(), address)}
				statsSequenceCount(t, e, old, 1)
				var dump bytes.Buffer
				if err := engine.Dump(context.Background(), e, &dump); err != nil {
					t.Fatal(err)
				}
				statsSequenceCount(t, e, old, 1)
				var late statistics.Writer
				late.Add(&e.Statistics, old, statistics.Fact{SequencePageAccesses: 17})
				var err error
				if control == "reset" {
					err = engine.Reset(context.Background(), e)
				} else {
					err = engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				read = e.CatalogRead()
				address, ok = read.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: s.TemporaryNamespace, Name: "stats_sequence_temp"})
				if !ok || read.MustNamespace(s.TemporaryNamespace).Temporary != s.SessionToken {
					t.Fatal("replacement lost owner TEMP sequence")
				}
				next := statistics.Key{Object: e.Catalog.Domain.Ref(read.Epoch(), address)}
				if next.Object.Address != old.Object.Address || next.Object.Epoch == old.Object.Epoch {
					t.Fatal("TEMP identity/epoch", old, next)
				}
				late.Flush(&e.Statistics, time.Now(), true)
				statsSequenceCount(t, e, old, 0)
				statsSequenceCount(t, e, next, 1)
				statsFunctionExec(t, s, "SELECT nextval('stats_sequence_temp')", "SELECT nextval('stats_sequence_temp')")
				statsSequenceCount(t, e, next, 1)
				var ending statistics.Writer
				ending.Add(&e.Statistics, next, statistics.Fact{SequencePageAccesses: 23})
				statsFunctionRetire(t, s)
				statsSequenceCount(t, e, next, 0)
				if e.CatalogRead().Get(next.Object.Address) != nil {
					t.Fatal("TEMP owner end left sequence descriptor")
				}
				ending.Flush(&e.Statistics, time.Now(), true)
				statsSequenceCount(t, e, next, 0)
				ending.Add(&e.Statistics, next, statistics.Fact{SequencePageAccesses: 29})
				ending.Flush(&e.Statistics, time.Now(), true)
				statsSequenceCount(t, e, next, 0)
			})
		})
	}
	for _, outcome := range []string{"create_abort", "create_drop", "drop_commit", "drop_abort"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			var key statistics.Key
			if outcome == "create_abort" || outcome == "create_drop" {
				statsFunctionExec(t, s, "BEGIN", "CREATE SEQUENCE stats_sequence_lifetime", "SELECT nextval('stats_sequence_lifetime')")
				e.Mu.Lock()
				private := s.Transactions.Catalog.PrivateRead(&e.Catalog.Owner, e.Catalog.Current().State())
				e.Mu.Unlock()
				key = statistics.Key{Object: enginetest.CatalogRef(t, e, private, "stats_sequence_lifetime")}
			} else {
				statsFunctionExec(t, s, "CREATE SEQUENCE stats_sequence_lifetime", "BEGIN", "SELECT nextval('stats_sequence_lifetime')", "COMMIT")
				key = statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_sequence_lifetime")}
				statsFunctionExec(t, s, "BEGIN", "DROP SEQUENCE stats_sequence_lifetime")
			}
			var late statistics.Writer
			late.Add(&e.Statistics, key, statistics.Fact{SequencePageAccesses: 19})
			switch outcome {
			case "create_abort", "drop_abort":
				statsFunctionExec(t, s, "ROLLBACK")
			case "create_drop":
				statsFunctionExec(t, s, "DROP SEQUENCE stats_sequence_lifetime", "COMMIT")
			case "drop_commit":
				statsFunctionExec(t, s, "COMMIT")
			}
			statsFunctionRetire(t, s)
			if outcome == "drop_abort" {
				statsSequenceCount(t, e, key, 1)
				return
			}
			late.Flush(&e.Statistics, time.Now(), true)
			late.Add(&e.Statistics, key, statistics.Fact{SequencePageAccesses: 23})
			late.Flush(&e.Statistics, time.Now(), true)
			statsSequenceCount(t, e, key, 0)
			if e.CatalogRead().Get(key.Object.Address) != nil {
				t.Fatal("retired sequence descriptor remains")
			}
		})
	}
}

func TestStatsSequenceSourceSelection(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "CREATE SEQUENCE stats_sequence_a", "CREATE SEQUENCE stats_sequence_b")
			a := enginetest.RoleTableRef(t, e, "stats_sequence_a")
			b := enginetest.RoleTableRef(t, e, "stats_sequence_b")
			sourceRef := enginetest.RoleTableRef(t, e, "pg_catalog.pg_statio_all_sequences")
			configureStats(s, mode)
			enginetest.TransactionBegin(t, s)
			op := enginetest.TransactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			read := auth.Catalog
			producer, ok := pgcatalog.ProducerFor(sourceRef.Address.ObjectID)
			if !ok {
				t.Fatal("missing sequence producer")
			}
			runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
			input := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: sourceRef, Source: read.MustRelation(sourceRef.Address), Runtime: runtime, Keep: pgcatalog.Keep{Column: 2, Test: func(sqltype.Value) bool { return false }}}
			rows, err := producer(input)
			if err != nil || len(rows) != 0 {
				t.Fatal("rejected keys", len(rows), err)
			}
			ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
			stamp, err := ctx.StatsSnapshotValue(false, enginetest.Usage(e, sqltype.Timestamptz).Type)
			if err != nil || !stamp.IsNull() {
				t.Fatal("rejected keys initialized statistics snapshot", stamp, err)
			}
			var writer statistics.Writer
			writer.Add(&e.Statistics, statistics.Key{Object: a}, statistics.Fact{SequencePageAccesses: 1})
			writer.Flush(&e.Statistics, time.Now(), true)
			input.Keep.Test = func(v sqltype.Value) bool { name, err := v.Text(); return err == nil && name == "stats_sequence_a" }
			rows, err = producer(input)
			if err != nil || len(rows) != 1 {
				t.Fatal("first A", len(rows), err)
			}
			retainedRead, retainedHit := rows[0][3], rows[0][4]
			writer.Add(&e.Statistics, statistics.Key{Object: a}, statistics.Fact{SequencePageAccesses: 1})
			writer.Add(&e.Statistics, statistics.Key{Object: b}, statistics.Fact{SequencePageAccesses: 2})
			writer.Flush(&e.Statistics, time.Now(), true)
			input.Keep.Test = func(v sqltype.Value) bool { name, err := v.Text(); return err == nil && name == "stats_sequence_b" }
			rows, err = producer(input)
			if err != nil || len(rows) != 1 {
				t.Fatal("first B", len(rows), err)
			}
			wantRead, wantHit := int64(1), int64(1)
			if mode == "snapshot" {
				wantRead, wantHit = 0, 0
			}
			gotRead, _ := rows[0][3].Integer()
			gotHit, _ := rows[0][4].Integer()
			if gotRead != wantRead || gotHit != wantHit {
				t.Fatal("producer fetched excluded B before Keep", gotRead, gotHit, wantRead, wantHit)
			}
			if n, _ := retainedRead.Integer(); n != 1 {
				t.Fatal("retained read changed", n)
			}
			if n, _ := retainedHit.Integer(); n != 0 {
				t.Fatal("retained hit changed", n)
			}
			if _, err := ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
				t.Fatal(err)
			}
			rows, err = producer(input)
			if err != nil || len(rows) != 1 {
				t.Fatal("cleared B", len(rows), err)
			}
			gotRead, _ = rows[0][3].Integer()
			gotHit, _ = rows[0][4].Integer()
			if gotRead != 1 || gotHit != 1 {
				t.Fatal("clear failed to recapture", gotRead, gotHit)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
