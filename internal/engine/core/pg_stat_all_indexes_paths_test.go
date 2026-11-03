package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
	"testing/synctest"
	"time"
)

func indexStatsSetup(t *testing.T) (*core.Engine, *session.Session, statistics.Key) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE TABLE stat_index_rows(n integer,payload text)", "INSERT INTO stat_index_rows VALUES(2,'a'),(1,'x'),(2,'b'),(NULL,NULL),(3,'z')", "CREATE INDEX stat_index_hash ON stat_index_rows USING hash(n)", "CREATE INDEX stat_index_btree ON stat_index_rows(n)", "SET enable_seqscan=off")
	return e, s, statistics.Key{Object: enginetest.RoleTableRef(t, e, "stat_index_hash")}
}
func indexStatsWant(t *testing.T, e *core.Engine, k statistics.Key, scans, entries, fetches int64) {
	t.Helper()
	f := e.Statistics.Read(k)
	if f.IndexScans != scans || f.IndexEntries != entries || f.IndexFetches != fetches || (scans == 0) != f.LastIndexScan.IsZero() {
		t.Fatalf("index %v fact %+v want %d/%d/%d", k, f, scans, entries, fetches)
	}
}
func indexStatsCapture(t *testing.T, e *core.Engine, s *session.Session, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	finishOp := func() {
		if !completed {
			completed = true
			op.Done()
		}
	}
	t.Cleanup(finishOp)
	query, source, release := enginetest.HashCaptureQuery(t, e, op, q)
	return query, source, func() { release(); finishOp() }
}
func TestPGStatAllIndexesActualPaths(t *testing.T) {
	for _, kind := range []string{"hit", "miss", "null", "empty", "residual_fallback", "sequential", "disabled", "enabled_then_off", "capture_only", "partial", "cancel_before_search", "cancel_after_search", "collision", "private", "historical"} {
		t.Run(kind, func(t *testing.T) {
			e, s, key := indexStatsSetup(t)
			want := []int64{0, 0, 0}
			q := "SELECT payload FROM stat_index_rows WHERE n=2"
			switch kind {
			case "miss":
				q = "SELECT payload FROM stat_index_rows WHERE n=$1"
			case "null":
				q = "SELECT payload FROM stat_index_rows WHERE n=$1"
			case "residual_fallback":
				q = "SELECT payload FROM stat_index_rows WHERE n=2 AND payload='a'"
			case "sequential":
				statsFunctionExec(t, s, "SET enable_indexscan=off")
			case "disabled":
				statsFunctionExec(t, s, "SET track_counts=off")
			case "empty":
				statsFunctionExec(t, s, "TRUNCATE stat_index_rows")
			case "private":
				statsFunctionExec(t, s, "BEGIN", "INSERT INTO stat_index_rows VALUES(2,'private')")
			case "historical":
				statsFunctionExec(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ", "SELECT n FROM stat_index_rows")
				enginetest.Run(t, e, "UPDATE stat_index_rows SET payload='new' WHERE n=2")
			}
			query, source, release := indexStatsCapture(t, e, s, q)
			if kind == "capture_only" {
				release()
				statsFunctionRetire(t, s)
				indexStatsWant(t, e, key, 0, 0, 0)
				return
			}
			if kind == "cancel_before_search" {
				exec.AdmitScan(query.Root, source)
				filter := exec.HashQueryFilter(query.Root)
				state := &exec.State{Source: source, HashFilter: filter, Frame: query.Root.(*plan.ProjectNode).Frame}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				stage, err := exec.OpenNode(ctx, state, filter)
				if err == nil {
					stage.Close()
					t.Fatal("canceled search was admitted")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("pre-search cancellation identity", err)
				}
				release()
				statsFunctionRetire(t, s)
				indexStatsWant(t, e, key, 0, 0, 0)
				return
			}
			var args []sqltype.Value
			if kind == "miss" || kind == "null" {
				u := source.HashIndexes[0].Usage
				v := sqltype.Null(u.Type)
				if kind == "miss" {
					var err error
					v, err = sqltype.Integer(u.Type, 99)
					if err != nil {
						t.Fatal(err)
					}
				}
				args = []sqltype.Value{v}
			}
			if kind == "collision" {
				original := source.HashIndexes[0].Root
				clone := *original
				clone.Buckets = maps.Clone(original.Buckets)
				probe := source.Committed[0].Values[0]
				h, err := clone.Binding.Provider.Hash(context.Background(), probe)
				if err != nil {
					t.Fatal(err)
				}
				one := source.Committed[1]
				clone.Buckets[h] = append(append([]storage.HashEntry(nil), clone.Buckets[h]...), storage.HashEntry{Key: one.Values[0], Row: one.Row, Version: one.Version, Position: 1}, storage.HashEntry{Key: probe, Row: one.Row, Version: one.Version, Position: 1})
				source.HashIndexes[0].Root = &clone
			}
			stage := hashFilterStage(t, query, source, args)
			actual := kind != "empty" && kind != "residual_fallback" && kind != "sequential" && kind != "private" && kind != "historical"
			if actual {
				if source.ScanDecision.Method != plan.HashCandidates || stage.HashCandidates == nil || stage.HashCandidates.Index != key.Object || !stage.HashCandidates.Simple {
					t.Fatal("not exact chosen simple hash", source.ScanDecision, stage.HashCandidates)
				}
			} else if stage.HashCandidates != nil {
				t.Fatal("fallback performed index search", kind)
			}
			count := 2
			switch kind {
			case "partial":
				if _, ok, err := stage.NextOccurrence(context.Background()); err != nil || !ok {
					t.Fatal("first demand", ok, err)
				}
				if _, err := stage.Rewind(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, ok, err := stage.NextOccurrence(context.Background()); err != nil || !ok {
					t.Fatal("retained replay", ok, err)
				}
				want = []int64{1, 2, 1}
			case "cancel_after_search":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := stage.Drain(ctx); !errors.Is(err, context.Canceled) {
					t.Fatal("cancel identity", err)
				}
				want = []int64{1, 2, 0}
			default:
				if kind == "miss" || kind == "null" || kind == "empty" {
					count = 0
				}
				if kind == "residual_fallback" {
					count = 1
				}
				if kind == "private" {
					count = 3
				}
				if err := stage.Drain(context.Background()); err != nil || stage.Count() != count {
					t.Fatal("SQL residual/bag", stage.Count(), count, err)
				}
				if actual && kind != "null" && kind != "disabled" {
					want = []int64{1, 2, 2}
					if kind == "miss" {
						want = []int64{1, 0, 0}
					}
					if kind == "collision" {
						want = []int64{1, 3, 3}
					}
				}
			}
			stage.Close()
			release()
			if kind == "private" || kind == "historical" {
				statsFunctionExec(t, s, "ROLLBACK")
			}
			if kind == "enabled_then_off" {
				statsFunctionExec(t, s, "SET track_counts=off")
			}
			statsFunctionRetire(t, s)
			indexStatsWant(t, e, key, want[0], want[1], want[2])
			indexStatsWant(t, e, statistics.Key{Object: enginetest.RoleTableRef(t, e, "stat_index_btree")}, 0, 0, 0)
		})
	}
}

func indexStatsBloomSetup(t *testing.T) (*core.Engine, *session.Session, statistics.Key, int) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE EXTENSION bloom", "CREATE TABLE stat_bloom(n integer)", "CREATE INDEX stat_bloom_idx ON stat_bloom USING bloom(n) WITH(length=1,col1=1)")
	_, root := bloomRoot(t, e, "stat_bloom_idx")
	// One-bit signatures have 16 possibilities. Find a real distinct collision
	// deterministically using the actual bound equality provider.
	seen := map[uint16]int{}
	a, b := -1, -1
	for n := range 256 {
		v, err := sqltype.Integer(root.Bloom.Bindings[0].Usage.Type, int64(n))
		if err != nil {
			t.Fatal(err)
		}
		mask := []uint16{0}
		if err := storage.AddBloomValue(context.Background(), mask, root.Bloom.Options, 0, &root.Bloom.Bindings[0].Provider, v); err != nil {
			t.Fatal(err)
		}
		if prior, ok := seen[mask[0]]; ok {
			a, b = prior, n
			break
		}
		seen[mask[0]] = n
	}
	if a < 0 {
		t.Fatal("bounded real signature collision not found")
	}
	statsFunctionExec(t, s, fmt.Sprintf("INSERT INTO stat_bloom VALUES(%d),(%d),(%d),(NULL)", a, a, b), "SET enable_seqscan=off")
	return e, s, statistics.Key{Object: enginetest.RoleTableRef(t, e, "stat_bloom_idx")}, a
}

func TestPGStatAllIndexesBloomAccess(t *testing.T) {
	e, s, key, a := indexStatsBloomSetup(t)
	query, source, release := indexStatsCapture(t, e, s, fmt.Sprintf("SELECT n FROM stat_bloom WHERE n=%d", a))
	stage := hashFilterStage(t, query, source, nil)
	if source.ScanDecision.Method != plan.BloomCandidates || stage.HashCandidates == nil || stage.HashCandidates.Simple || len(stage.HashCandidates.Occurrences) != 3 {
		t.Fatal("not real lossy candidate stream", source.ScanDecision, stage.HashCandidates)
	}
	if err := stage.Drain(context.Background()); err != nil || stage.Count() != 2 {
		t.Fatal("original exact recheck", stage.Count(), err)
	}
	stage.Close()
	release()
	statsFunctionRetire(t, s)
	indexStatsWant(t, e, key, 1, 3, 0)
	fallback := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, fallback, "SET enable_seqscan=off", "SET enable_indexscan=off")
	fallbackQuery, fallbackSource, closeFallback := indexStatsCapture(t, e, fallback, fmt.Sprintf("SELECT n FROM stat_bloom WHERE n=%d", a))
	fallbackStage := hashFilterStage(t, fallbackQuery, fallbackSource, nil)
	if fallbackSource.ScanDecision.Method != plan.FullScan || fallbackStage.HashCandidates != nil {
		t.Fatal("disabled bulk path searched")
	}
	if err := fallbackStage.Drain(context.Background()); err != nil || fallbackStage.Count() != 2 {
		t.Fatal("bulk full-scan fallback bag", err)
	}
	fallbackStage.Close()
	closeFallback()
	statsFunctionRetire(t, fallback)
	indexStatsWant(t, e, key, 1, 3, 0)
	empty := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, empty, "TRUNCATE stat_bloom", "SET enable_seqscan=off")
	emptyQuery, emptySource, closeEmpty := indexStatsCapture(t, e, empty, fmt.Sprintf("SELECT n FROM stat_bloom WHERE n=%d", a))
	emptyStage := hashFilterStage(t, emptyQuery, emptySource, nil)
	if emptySource.ScanDecision.Method != plan.BloomCandidates || emptyStage.HashCandidates == nil || emptyStage.HashCandidates.Bloom == nil {
		t.Fatal("empty real signature traversal not selected", emptySource.ScanDecision)
	}
	if err := emptyStage.Drain(context.Background()); err != nil || emptyStage.Count() != 0 {
		t.Fatal("empty signature traversal", err)
	}
	emptyStage.Close()
	closeEmpty()
	statsFunctionRetire(t, empty)
	indexStatsWant(t, e, key, 2, 3, 0)
}

func TestPGStatAllIndexesTransactionStop(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint("implicit_abort_", failed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, setup, key := indexStatsSetup(t)
				statsFunctionRetire(t, setup)
				actor := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, actor, "SET enable_seqscan=off")
				held := bufferUnitCapture(t, e, actor, "SELECT payload FROM stat_index_rows WHERE n=2")
				if err := exec.SelectQuerySources(context.Background(), held.source); err != nil {
					t.Fatal(err)
				}
				stage := hashFilterStage(t, &plan.Query{Root: held.root}, held.source, nil)
				if stage.HashCandidates == nil || stage.HashCandidates.Index != key.Object {
					t.Fatal("genuine implicit search missing")
				}
				if err := stage.Drain(context.Background()); err != nil || stage.Count() != 2 {
					t.Fatal("actual read", err)
				}
				start := time.Now()
				time.Sleep(2 * time.Second)
				synctest.Wait()
				indexStatsWant(t, e, key, 0, 0, 0)
				stop := time.Now().UTC().Truncate(time.Microsecond)
				stage.Close()
				held.source.Close()
				var terminal error
				if failed {
					terminal = context.Canceled
				}
				if err := held.scope.Finish(terminal); !errors.Is(err, terminal) {
					t.Fatal("terminal error identity", err, terminal)
				}
				held.closed = true
				held.op.Done()
				statsFunctionRetire(t, actor)
				indexStatsWant(t, e, key, 1, 2, 2)
				if f := e.Statistics.Read(key); !f.LastIndexScan.Equal(stop) || !f.LastIndexScan.After(start) {
					t.Fatal("scan/flush clock replaced terminal stop", f.LastIndexScan, stop)
				}
			})
		})
	}
	for _, terminal := range []string{"COMMIT", "ROLLBACK", "PREPARE TRANSACTION 'index_stop'", "failure", "savepoint", "pl_exception"} {
		t.Run(terminal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, setup, key := indexStatsSetup(t)
				statsFunctionRetire(t, setup)
				s := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, s, "SET enable_seqscan=off", "SELECT payload FROM stat_index_rows WHERE n=2")
				indexStatsWant(t, e, key, 1, 2, 2)
				// Genuine second implicit read remains throttled, then explicit work falls
				// back. This is never presented as an indexed explicit transaction scan.
				statsFunctionExec(t, s, "SELECT payload FROM stat_index_rows WHERE n=2", "BEGIN", "SELECT payload FROM stat_index_rows WHERE n=2")
				time.Sleep(2 * time.Second)
				synctest.Wait()
				indexStatsWant(t, e, key, 1, 2, 2)
				stop := time.Now().UTC().Truncate(time.Microsecond)
				switch terminal {
				case "failure":
					if err := statsFunctionSQL(t, s, "SELECT missing FROM stat_index_rows"); err == nil {
						t.Fatal("missing column admitted")
					}
					time.Sleep(time.Second)
					statsFunctionExec(t, s, "ROLLBACK")
				case "savepoint":
					statsFunctionExec(t, s, "SAVEPOINT child", "ROLLBACK TO child")
					indexStatsWant(t, e, key, 1, 2, 2)
					time.Sleep(time.Second)
					stop = time.Now().UTC().Truncate(time.Microsecond)
					statsFunctionExec(t, s, "COMMIT")
				case "pl_exception":
					statsFunctionExec(t, s, "DO $$BEGIN BEGIN RAISE EXCEPTION 'discard'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; END$$")
					indexStatsWant(t, e, key, 1, 2, 2)
					time.Sleep(time.Second)
					stop = time.Now().UTC().Truncate(time.Microsecond)
					statsFunctionExec(t, s, "COMMIT")
				default:
					statsFunctionExec(t, s, terminal)
				}
				statsFunctionRetire(t, s)
				indexStatsWant(t, e, key, 2, 4, 4)
				if f := e.Statistics.Read(key); !f.LastIndexScan.Equal(stop) {
					t.Fatal("prior implicit batch terminal marker", stop, f.LastIndexScan)
				}
				if terminal == "PREPARE TRANSACTION 'index_stop'" {
					finisher := enginetest.StartSession(t, e, nil)
					statsFunctionExec(t, finisher, "COMMIT PREPARED 'index_stop'")
					statsFunctionRetire(t, finisher)
					indexStatsWant(t, e, key, 2, 4, 4)
				}
			})
		})
	}
	synctest.Test(t, func(t *testing.T) {
		e, s, key := indexStatsSetup(t)
		statsFunctionRetire(t, s)
		actor := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, actor, "SET enable_seqscan=off", "SELECT payload FROM stat_index_rows WHERE n=2")
		first := e.Statistics.Read(key)
		statsFunctionExec(t, actor, "SELECT payload FROM stat_index_rows WHERE n=2")
		time.Sleep(100 * time.Millisecond)
		statsFunctionExec(t, actor, "SELECT 1")
		latest := time.Now().UTC().Truncate(time.Microsecond)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		got := e.Statistics.Read(key)
		if got.IndexScans != first.IndexScans+1 || !got.LastIndexScan.Equal(latest) {
			t.Fatal("throttled batch did not use latest stop", first, got, latest)
		}
		statsFunctionExec(t, actor, "SELECT 1")
		statsFunctionRetire(t, actor)
		if !reflect.DeepEqual(e.Statistics.Read(key), got) {
			t.Fatal("no-scan stop changed last scan")
		}
	})
}

// Private invariant-failure clones exercise real executor error exits. They
// never publish corrupt roots or expose a public SQL corruption mechanism.
func TestPGStatAllIndexesInterruptedAccess(t *testing.T) {
	for _, kind := range []string{"hash_enumeration_error", "hash_partial_heap_cancel", "bloom_enumeration_error"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var e *core.Engine
				var setup *session.Session
				var key statistics.Key
				q := "SELECT payload FROM stat_index_rows WHERE n=2"
				control := "stat_index_btree"
				if kind == "bloom_enumeration_error" {
					var probe int
					e, setup, key, probe = indexStatsBloomSetup(t)
					control = "stat_bloom_control"
					statsFunctionExec(t, setup, "CREATE INDEX stat_bloom_control ON stat_bloom(n)")
					q = fmt.Sprintf("SELECT n FROM stat_bloom WHERE n=%d", probe)
				} else {
					e, setup, key = indexStatsSetup(t)
				}
				statsFunctionRetire(t, setup)
				actor := enginetest.StartSession(t, e, nil)
				statsFunctionExec(t, actor, "SET enable_seqscan=off")
				held := bufferUnitCapture(t, e, actor, q)
				if err := exec.SelectQuerySources(context.Background(), held.source); err != nil {
					t.Fatal(err)
				}
				query := &plan.Query{Root: held.root}
				exec.AdmitScan(query.Root, held.source)
				method := plan.HashCandidates
				if kind == "bloom_enumeration_error" {
					method = plan.BloomCandidates
				}
				if held.source.ScanDecision.Method != method || held.source.ScanDecision.Index != key.Object.Address {
					t.Fatal("interrupted search lacks actual selected identity", held.source.ScanDecision, key)
				}
				var originalEntries, originalSnapshot any
				switch kind {
				case "hash_enumeration_error":
					capture := &held.source.HashIndexes[0]
					original := capture.Root
					clone := *original
					clone.Buckets = maps.Clone(original.Buckets)
					hash, err := clone.Binding.Provider.Hash(context.Background(), held.source.Committed[0].Values[0])
					if err != nil {
						t.Fatal(err)
					}
					bucket := append([]storage.HashEntry(nil), original.Buckets[hash]...)
					if len(bucket) != 2 {
						t.Fatal("expected two genuine accepted hash entries", bucket)
					}
					for _, entry := range bucket {
						if entry.Position < 0 || entry.Position >= len(held.source.Committed) {
							t.Fatal("original hash candidate position", entry)
						}
						row := held.source.Committed[entry.Position]
						equal, err := clone.Binding.Provider.Equal(context.Background(), entry.Key, held.source.Committed[0].Values[0])
						if err != nil || !equal || row.Row != entry.Row || row.Version != entry.Version {
							t.Fatal("original hash candidate key/identity", entry, err)
						}
					}
					originalEntries = original.Buckets[hash]
					originalSnapshot = append([]storage.HashEntry(nil), original.Buckets[hash]...)
					// Keep a genuine matching suffix beyond the invariant error.
					bucket = append(bucket, bucket[0])
					// Only the private clone's second entry is invalid. The first
					// must be accepted before the real enumeration rejects it.
					bucket[1].Position = len(held.source.Committed)
					clone.Buckets[hash] = bucket
					capture.Root = &clone
					if original.Buckets[hash][1].Position == bucket[1].Position {
						t.Fatal("published hash root was changed")
					}
				case "bloom_enumeration_error":
					capture := &held.source.BloomIndexes[0]
					original := capture.Root
					clone := *original
					clone.Entries = append([]storage.IndexEntry(nil), original.Entries...)
					if len(clone.Entries) != 4 || len(clone.Bloom.Entries) != 4 {
						t.Fatal("expected the genuine duplicate/collision/NULL fixture", clone.Entries)
					}
					mask := make([]uint16, clone.Bloom.Options.Words)
					if err := storage.AddBloomValue(context.Background(), mask, clone.Bloom.Options, 0, &clone.Bloom.Bindings[0].Provider, held.source.Committed[0].Values[0]); err != nil {
						t.Fatal(err)
					}
					matching := make([]int, 0, 3)
					for i, signed := range clone.Bloom.Entries {
						if !storage.BloomContains(signed.Signature, mask) {
							continue
						}
						entry := clone.Entries[i]
						if entry.Position < 0 || entry.Position >= len(held.source.Committed) {
							t.Fatal("original bloom candidate/signature", i, entry)
						}
						row := held.source.Committed[entry.Position]
						if row.Row != entry.Row || row.Version != entry.Version {
							t.Fatal("original bloom candidate identity", i, entry)
						}
						matching = append(matching, i)
					}
					if len(matching) != 3 {
						t.Fatal("missing duplicate/collision matching prefix and suffix", matching)
					}
					originalEntries = original.Entries
					originalSnapshot = append([]storage.IndexEntry(nil), original.Entries...)
					clone.Entries[matching[1]].Position = len(held.source.Committed)
					capture.Root = &clone
					if original.Entries[matching[1]].Position == clone.Entries[matching[1]].Position {
						t.Fatal("published bloom root was changed")
					}
				}
				filter := exec.HashQueryFilter(query.Root)
				if filter == nil {
					t.Fatal("missing actual filter")
				}
				state := &exec.State{Source: held.source, HashFilter: filter, Frame: query.Root.(*plan.ProjectNode).Frame, Input: held.scope.Input, Output: held.scope.Output}
				start := time.Now()
				stage, terminal := exec.OpenNode(context.Background(), state, filter)
				stageOpened := terminal == nil
				if stageOpened {
					defer func() {
						if !stage.Closed {
							stage.Close()
						}
					}()
				}
				wantEntries, wantFetches := int64(1), int64(0)
				if kind == "hash_partial_heap_cancel" {
					if terminal != nil {
						t.Fatal(terminal)
					}
					if stage.HashCandidates == nil || stage.HashCandidates.Index != key.Object || !stage.HashCandidates.Simple || len(stage.HashCandidates.Occurrences) != 2 {
						t.Fatal("not the genuine two-entry simple search", stage.HashCandidates)
					}
					if _, ok, err := stage.NextOccurrence(context.Background()); err != nil || !ok {
						t.Fatal("first heap demand", ok, err)
					}
					// A failing demand closes/releases its stage. Assert retained
					// output while open and retain the candidate owner separately
					// to prove cancellation does not consume the second fetch.
					candidates := stage.HashCandidates
					if stage.Count() != 1 || candidates.Position != 1 || candidates.Fetched != 1 {
						t.Fatal("first demand did not retain exactly one heap candidate", stage.Count(), candidates)
					}
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					terminal = stage.Drain(ctx)
					if !errors.Is(terminal, context.Canceled) || !stage.Closed || stage.Terminal != terminal || candidates.Position != 1 || candidates.Fetched != 1 {
						t.Fatal("canceled demand lost its nonempty incomplete prefix", stage.Closed, candidates, terminal)
					}
					wantEntries, wantFetches = 2, 1
				} else {
					if stageOpened {
						t.Fatal("invalid second candidate did not interrupt enumeration")
					}
					wantError := "stuntgres: invalid hash candidate position"
					if kind == "bloom_enumeration_error" {
						wantError = "stuntgres: invalid bloom candidate position"
					}
					if terminal.Error() != wantError {
						t.Fatal("enumeration error identity", terminal, wantError)
					}
				}
				if !reflect.DeepEqual(originalEntries, originalSnapshot) {
					t.Fatal("enumeration changed the original root backing", originalEntries, originalSnapshot)
				}
				// Enumeration/consumption failed at A, but the real implicit
				// statement abort occurs at B. Neither A nor flush time owns last.
				time.Sleep(2 * time.Second)
				synctest.Wait()
				indexStatsWant(t, e, key, 0, 0, 0)
				stop := time.Now().UTC().Truncate(time.Microsecond)
				held.source.Close()
				finished := held.scope.Finish(terminal)
				held.closed = true
				held.op.Done()
				if finished != terminal {
					t.Fatal("abort did not preserve the actual execution error", terminal, finished)
				}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				publication := time.Now().UTC().Truncate(time.Microsecond)
				statsFunctionExec(t, actor, "SELECT 1")
				statsFunctionRetire(t, actor)
				indexStatsWant(t, e, key, 1, wantEntries, wantFetches)
				indexStatsWant(t, e, statistics.Key{Object: enginetest.RoleTableRef(t, e, control)}, 0, 0, 0)
				if fact := e.Statistics.Read(key); !fact.LastIndexScan.Equal(stop) || !fact.LastIndexScan.After(start) || fact.LastIndexScan.Equal(publication) {
					t.Fatal("interrupted prefix did not retain its terminal stop", fact, start, stop, publication)
				}
			})
		})
	}
}
