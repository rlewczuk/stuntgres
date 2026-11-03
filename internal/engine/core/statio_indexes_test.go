package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func statioKey(t *testing.T, e *core.Engine, name string) statistics.Key {
	t.Helper()
	return statistics.Key{Object: enginetest.RoleTableRef(t, e, name), Category: statistics.Relation}
}
func statioFact(t *testing.T, e *core.Engine, key statistics.Key, read, hit int64) {
	t.Helper()
	f := e.Statistics.Read(key)
	if f.IndexBlocksRead != read || f.IndexBlocksHit != hit {
		t.Fatalf("%+v read/hit=%d/%d want%d/%d", key, f.IndexBlocksRead, f.IndexBlocksHit, read, hit)
	}
}
func statioSeed(t *testing.T, e *core.Engine) {
	t.Helper()
	for _, q := range []string{"CREATE TABLE statio_events(k integer,v integer)", "INSERT INTO statio_events VALUES(1,10),(2,20)", "CREATE INDEX statio_events_i ON statio_events USING hash(k)", "CREATE INDEX statio_events_unused ON statio_events USING hash(v)"} {
		enginetest.Run(t, e, q)
	}
}
func statioOpen(t *testing.T, c *bufferCapture, method plan.ScanMethod) *exec.Stage {
	t.Helper()
	if err := exec.SelectQuerySources(c.op.Ctx, c.source); err != nil {
		t.Fatal(err)
	}
	exec.AdmitScan(c.root, c.source)
	if c.source.ScanDecision.Method != method {
		t.Fatal("actual method", c.source.ScanDecision)
	}
	state := exec.State{Source: c.source, HashFilter: exec.HashQueryFilter(c.root), Retain: true}
	stage, err := exec.OpenNode(c.op.Ctx, &state, c.root)
	if err != nil {
		t.Fatal(err)
	}
	return &stage
}

func TestStatioAllIndexesRuntimeEvents(t *testing.T) {
	for _, repeat := range []string{"first", "isolated_repeat"} {
		t.Run(repeat, func(t *testing.T) {
			e := engine.New()
			statioSeed(t, e)
			key, unused := statioKey(t, e, "statio_events_i"), statioKey(t, e, "statio_events_unused")
			for i := int64(0); i < 3; i++ {
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "SET enable_seqscan=off")
				enginetest.SessionSQL(t, s, "SET track_io_timing=off")
				c := bufferUnitCapture(t, e, s, "SELECT k FROM statio_events WHERE k=1")
				statioFact(t, e, key, min(i, 1), max(i-1, 0))
				stage := statioOpen(t, c, plan.HashCandidates)
				row := make([]sqltype.Value, 1)
				if present, err := stage.Next(c.op.Ctx, row); err != nil || !present {
					t.Fatal("actual result", present, err)
				}
				if n, err := row[0].Integer(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				if _, err := stage.Rewind(c.op.Ctx); err != nil {
					t.Fatal(err)
				}
				if present, err := stage.Next(c.op.Ctx, row); err != nil || !present {
					t.Fatal("rewind", present, err)
				}
				stage.Close()
				c.close(t)
				statsFunctionRetire(t, s)
				statioFact(t, e, key, 1, i)
				statioFact(t, e, unused, 0, 0)
				requests := e.Statistics.Read(key)
				if requests.BlocksFetched != i+1 || requests.BlocksHit != i+1 {
					t.Fatal("logical cache outcome changed generic index requests", requests)
				}
			}
		})
	}
	t.Run("policy_at_open_and_repeated_capture", func(t *testing.T) {
		e := engine.New()
		statioSeed(t, e)
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "SET enable_seqscan=off")
		c := bufferUnitCapture(t, e, s, "SELECT k FROM statio_events WHERE k=1")
		// Apply LOCAL policy through the ordinary settings authority after an
		// eligible implicit capture, not by requesting an explicit hash scan.
		store := c.op.EffectiveSettings()
		localCounts := func(input string) {
			definition, _ := e.Settings.Lookup("track_counts")
			value, err := definition.Parse(input)
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			store.Mu.Lock()
			s.Mu.Lock()
			_, err = store.ApplyLocked(c.op, core.DomainOf(e), e.Settings, definition, &value, nil, session.SettingLocal)
			s.Mu.Unlock()
			store.Mu.Unlock()
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
		}
		localCounts("off")
		first := statioOpen(t, c, plan.HashCandidates)
		first.Close()
		localCounts("on")
		second := statioOpen(t, c, plan.HashCandidates)
		second.Close()
		// Open while pinned must count another hit without another pin.
		for _, index := range c.source.HashIndexes {
			if index.Address == statioKey(t, e, "statio_events_i").Object.Address {
				index.ReadLease.Open()
			}
		}
		c.close(t)
		statsFunctionRetire(t, s)
		statioFact(t, e, statioKey(t, e, "statio_events_i"), 0, 2)
	})
	t.Run("no_probe_no_event", func(t *testing.T) {
		e := engine.New()
		statioSeed(t, e)
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "SELECT k FROM statio_events")
		enginetest.SessionSQL(t, s, "SET enable_seqscan=off")
		enginetest.SessionSQL(t, s, "SELECT k FROM statio_events WHERE k=NULL::integer")
		enginetest.SessionSQL(t, s, "CREATE TABLE statio_empty(k integer)")
		enginetest.SessionSQL(t, s, "CREATE INDEX statio_empty_i ON statio_empty USING hash(k)")
		enginetest.SessionSQL(t, s, "SELECT k FROM statio_empty WHERE k=1")
		c := bufferUnitCapture(t, e, s, "SELECT k FROM statio_events WHERE k=1")
		c.close(t)
		statsFunctionRetire(t, s)
		for _, name := range []string{"statio_events_i", "statio_events_unused", "statio_empty_i"} {
			statioFact(t, e, statioKey(t, e, name), 0, 0)
		}
	})
	t.Run("bloom_consumer", func(t *testing.T) {
		e := engine.New()
		for _, q := range []string{"CREATE EXTENSION bloom", "CREATE TABLE statio_bloom(k integer)", "INSERT INTO statio_bloom VALUES(1),(2)", "CREATE INDEX statio_bloom_i ON statio_bloom USING bloom(k)"} {
			enginetest.Run(t, e, q)
		}
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "SET enable_seqscan=off")
		c := bufferUnitCapture(t, e, s, "SELECT k FROM statio_bloom WHERE k=1")
		stage := statioOpen(t, c, plan.BloomCandidates)
		if err := stage.Drain(c.op.Ctx); err != nil || stage.Count() != 1 {
			t.Fatal("bloom actual result", stage.Count(), err)
		}
		stage.Close()
		c.close(t)
		statsFunctionRetire(t, s)
		statioFact(t, e, statioKey(t, e, "statio_bloom_i"), 1, 0)
	})
	t.Run("admitted_read_survives_cancel", func(t *testing.T) {
		e := bufferUnitEngine(t)
		statioSeed(t, e)
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "SET enable_seqscan=off")
		c := bufferUnitCapture(t, e, s, "SELECT k FROM statio_events WHERE k=1")
		stage := statioOpen(t, c, plan.HashCandidates)
		ctx, cancel := context.WithCancel(c.op.Ctx)
		cancel()
		if _, err := stage.Next(ctx, make([]sqltype.Value, 1)); !errors.Is(err, context.Canceled) {
			t.Fatal("cancel lost", err)
		}
		stage.Close()
		c.close(t)
		statsFunctionRetire(t, s)
		statioFact(t, e, statioKey(t, e, "statio_events_i"), 1, 0)
		observer := enginetest.StartSession(t, e, nil)
		bufferUnitFact(t, e, observer, bufferUnitNode(t, e, "statio_events_i"), 1, 0)
	})
	t.Run("admitted_read_survives_error", func(t *testing.T) {
		e := bufferUnitEngine(t)
		statioSeed(t, e)
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "SET enable_seqscan=off")
		if err := statsFunctionSQL(t, s, "SELECT 1/(k-k) FROM statio_events WHERE k=1"); err == nil {
			t.Fatal("actual post-read expression error missing")
		}
		statsFunctionRetire(t, s)
		statioFact(t, e, statioKey(t, e, "statio_events_i"), 1, 0)
		observer := enginetest.StartSession(t, e, nil)
		bufferUnitFact(t, e, observer, bufferUnitNode(t, e, "statio_events_i"), 1, 0)
	})
}

func TestStatioAllIndexesLifetime(t *testing.T) {
	t.Run("private_boundaries", statioPrivateLifetime)
	for _, finish := range []string{"rollback", "commit", "prepared_rollback", "prepared_commit", "savepoint"} {
		t.Run(finish, func(t *testing.T) {
			e := engine.New()
			statioSeed(t, e)
			key, control := statioKey(t, e, "statio_events_i"), statioKey(t, e, "statio_events_unused")
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "SET enable_seqscan=off")
			enginetest.SessionSQL(t, s, "SELECT k FROM statio_events WHERE k=1")
			enginetest.SessionSQL(t, s, "SELECT v FROM statio_events WHERE v=10")
			statsFunctionRetire(t, s)
			statioFact(t, e, key, 1, 0)
			statioFact(t, e, control, 1, 0)
			actor := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, actor, "BEGIN")
			if finish == "savepoint" {
				statsFunctionExec(t, actor, "SAVEPOINT keep")
			}
			statsFunctionExec(t, actor, "DROP INDEX statio_events_i")
			statioFact(t, e, key, 1, 0)
			var late statistics.Writer
			late.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, true)
			switch finish {
			case "prepared_commit", "prepared_rollback":
				statsFunctionExec(t, actor, "PREPARE TRANSACTION 'statio_drop'")
				statioFact(t, e, key, 1, 0)
				finisher := enginetest.StartSession(t, e, nil)
				q := "ROLLBACK PREPARED 'statio_drop'"
				if finish == "prepared_commit" {
					q = "COMMIT PREPARED 'statio_drop'"
				}
				statsFunctionExec(t, finisher, q)
			case "savepoint":
				statsFunctionExec(t, actor, "ROLLBACK TO keep", "COMMIT")
			case "commit":
				statsFunctionExec(t, actor, "COMMIT")
			default:
				statsFunctionExec(t, actor, "ROLLBACK")
			}
			late.Flush(&e.Statistics, time.Now(), true)
			statsFunctionRetire(t, actor)
			removed := finish == "commit" || finish == "prepared_commit"
			if removed {
				statioFact(t, e, key, 0, 0)
				enginetest.Run(t, e, "CREATE INDEX statio_events_i ON statio_events USING hash(k)")
				successor := statioKey(t, e, "statio_events_i")
				if successor == key {
					t.Fatal("successor identity aliased")
				}
				statioFact(t, e, successor, 0, 0)
				fresh := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, fresh, "SET enable_seqscan=off", "SELECT k FROM statio_events WHERE k=1")
				statsFunctionRetire(t, fresh)
				statioFact(t, e, successor, 1, 0)
			} else {
				statioFact(t, e, key, 1, 1)
			}
			statioFact(t, e, control, 1, 0)
		})
	}
	t.Run("metadata_and_storage_incarnations", func(t *testing.T) {
		e := engine.New()
		statioSeed(t, e)
		key := statioKey(t, e, "statio_events_i")
		read := func() {
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "SET enable_seqscan=off", "SELECT k FROM statio_events WHERE k=1")
			statsFunctionRetire(t, s)
		}
		read()
		statioFact(t, e, key, 1, 0)
		enginetest.Run(t, e, "ALTER INDEX statio_events_i RENAME TO statio_renamed")
		if statioKey(t, e, "statio_renamed") != key {
			t.Fatal("metadata reminted identity")
		}
		read()
		statioFact(t, e, key, 1, 1)
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "REINDEX INDEX statio_renamed")
		statsFunctionRetire(t, s)
		statioFact(t, e, key, 1, 1)
		read()
		statioFact(t, e, key, 2, 1)
	})
}
