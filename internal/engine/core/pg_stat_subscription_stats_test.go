package core_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var subscriptionStatsLocal = [...]int64{11, 23, 37, 41, 53, 67, 71, 83, 97}
var subscriptionStatsPeer = [...]int64{101, 103, 107, 109, 113, 127, 131, 137, 139}

func subscriptionStatsSeed(e *core.Engine, key statistics.Key, vector [9]int64) statistics.Fact {
	var writer statistics.Writer
	writer.Add(&e.Statistics, key, statistics.Fact{ApplyErrorCount: vector[0], SyncErrorCount: vector[1], ConflInsertExists: vector[2], ConflUpdateOriginDiffers: vector[3], ConflUpdateExists: vector[4], ConflUpdateMissing: vector[5], ConflDeleteOriginDiffers: vector[6], ConflDeleteMissing: vector[7], ConflMultipleUniqueConflicts: vector[8]})
	writer.Flush(&e.Statistics, time.Now(), true)
	return e.Statistics.Read(key)
}

func subscriptionStatsOwned(t *testing.T, result core.Result, want map[ids.OID]statistics.Fact) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("no owned cumulative rows")
	}
	defer result.Rows.Close()
	columns := result.Rows.Schema()
	if len(columns) != 12 {
		t.Fatal("twelve source attributes", columns)
	}
	seen := make(map[ids.OID]bool)
	var row [12]sqltype.Value
	for {
		present, err := result.Rows.Next(t.Context(), row[:])
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		oid, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		fact, ok := want[ids.OID(oid)]
		if !ok || seen[ids.OID(oid)] {
			t.Fatal("unexpected/duplicate actual subscription", oid)
		}
		seen[ids.OID(oid)] = true
		for i, value := range row {
			if value.Type() != columns[i].Usage.Type || value.IsNull() != (i == 11 && fact.ResetAt.IsZero()) {
				t.Fatal("typed retained field", i, value)
			}
		}
		vector := [...]int64{fact.ApplyErrorCount, fact.SyncErrorCount, fact.ConflInsertExists, fact.ConflUpdateOriginDiffers, fact.ConflUpdateExists, fact.ConflUpdateMissing, fact.ConflDeleteOriginDiffers, fact.ConflDeleteMissing, fact.ConflMultipleUniqueConflicts}
		for i, want := range vector {
			n, err := row[i+2].Integer()
			if err != nil || n != want {
				t.Fatal("distinct actual provider field", oid, i, n, want, err)
			}
		}
		if !fact.ResetAt.IsZero() {
			stamp, err := sqltype.TimestampFromTime(columns[11].Usage.Type, fact.ResetAt)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := row[11].Time()
			b, _ := stamp.Time()
			if a != b {
				t.Fatal("exact reset instant", a, b)
			}
		}
	}
	if len(seen) != len(want) {
		t.Fatal("descriptor rows, not provider keys", seen, want)
	}
}

func subscriptionStatsCapture(t *testing.T, e *core.Engine, s *session.Session) core.Result {
	t.Helper()
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_subscription_stats")
	result, err := enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Use the real imported frame on every execution, including a retained description.
func subscriptionStatsNameExecute(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, description *core.Description, name *string) (core.Result, error) {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return core.Result{}, err
	}
	scope.Expected = description
	result, err := scope.Execute(ast, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
		if info.Count != 1 || info.Types[0].Type.ID() != sqltype.Name {
			t.Fatal("catalog name parameter must retain its inferred NAME type", info)
		}
		if name == nil {
			frame[0] = sqltype.Null(info.Types[0].Type)
			return nil
		}
		value, err := info.Types[0].Type.InputWithEnvironment(ctx, *name, -1, input)
		frame[0] = value
		return err
	})
	err = scope.Finish(err)
	return result, err
}

func TestPGStatSubscriptionStatsProvider(t *testing.T) {
	t.Run("ResetAndPending", func(t *testing.T) {
		e, s, peer, _, keys := resetStatsFixture(t)
		key := keys[0]
		subscriptionStatsSeed(e, key, subscriptionStatsLocal)
		untouched := subscriptionStatsSeed(peer, keys[2], subscriptionStatsPeer)
		var pending statistics.Writer
		pending.Add(&e.Statistics, key, resetStatsVector(301))
		resetStatsExec(t, s, fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)", key.Object.Address.ObjectID))
		at := resetStatsZero(t, e, key)
		generation := e.Statistics.Generation()
		if !pending.Pending() || peer.Statistics.Read(keys[2]) != untouched {
			t.Fatal("targeted reset changed pending/peer authority")
		}
		pending.Flush(&e.Statistics, time.Now(), true)
		want := resetStatsVector(301)
		want.ResetAt = at
		want.Generation = generation
		if got := e.Statistics.Read(key); got != want || pending.Pending() {
			t.Fatal("pending publication field/reset mapping", got, want)
		}
		subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), map[ids.OID]statistics.Fact{key.Object.Address.ObjectID: want, keys[1].Object.Address.ObjectID: e.Statistics.Read(keys[1]), keys[2].Object.Address.ObjectID: untouched})
	})
	e, s, peer, ps, keys := resetStatsFixture(t)
	a, c := keys[0], keys[2]
	local := subscriptionStatsSeed(e, a, subscriptionStatsLocal)
	remote := subscriptionStatsSeed(peer, c, subscriptionStatsPeer)
	want := map[ids.OID]statistics.Fact{a.Object.Address.ObjectID: local, keys[1].Object.Address.ObjectID: e.Statistics.Read(keys[1]), c.Object.Address.ObjectID: remote}
	projected := c
	projected.Object = e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), c.Object.Address)
	resetStatsAbsent(t, e, projected)
	for _, pair := range []struct {
		e *core.Engine
		s *session.Session
	}{{e, s}, {peer, ps}} {
		subscriptionStatsOwned(t, subscriptionStatsCapture(t, pair.e, pair.s), want)
	}
	orphan := a
	orphan.Object.Address.ObjectID += 1000000
	e.Statistics.EnsureSubscription(orphan)
	subscriptionStatsSeed(e, orphan, subscriptionStatsPeer)
	subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), want)
	if e.Statistics.Read(a) != local || peer.Statistics.Read(c) != remote {
		t.Fatal("read mutated actual event facts")
	}
	old := subscriptionStatsCapture(t, e, s)
	oldType := old.Rows.Schema()[1].Usage.Type
	statsFunctionExec(t, s, "ALTER SUBSCRIPTION reset_target_a RENAME TO stats_provider_renamed", "BEGIN", "DROP SUBSCRIPTION stats_provider_renamed", "ROLLBACK")
	if resetStatsKey(t, e, s, "stats_provider_renamed") != a || e.Statistics.Read(a) != local {
		t.Fatal("rename/rolled back DROP lost qualified history")
	}
	var late statistics.Writer
	late.Add(&e.Statistics, a, resetStatsVector(200))
	statsFunctionExec(t, s, "DROP SUBSCRIPTION stats_provider_renamed")
	late.Flush(&e.Statistics, time.Now(), true)
	resetStatsAbsent(t, e, a)
	delete(want, a.Object.Address.ObjectID)
	subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), want)
	fresh := resetStatsCreate(t, e, s, "reset_target_a")
	if fresh == a {
		t.Fatal("same-name successor reused identity")
	}
	want[fresh.Object.Address.ObjectID] = e.Statistics.Read(fresh)
	subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), want)
	statsFunctionExec(t, s, "BEGIN")
	aborted := resetStatsCreate(t, e, s, "stats_provider_aborted")
	statsFunctionExec(t, s, "ROLLBACK")
	resetStatsAbsent(t, e, aborted)
	if old.Rows.Schema()[1].Usage.Type != oldType {
		t.Fatal("retained schema lost original generation")
	}
	oldWant := map[ids.OID]statistics.Fact{a.Object.Address.ObjectID: local, keys[1].Object.Address.ObjectID: e.Statistics.Read(keys[1]), c.Object.Address.ObjectID: remote}
	subscriptionStatsOwned(t, old, oldWant)
}

func TestPGStatSubscriptionStatsSnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, route := range []string{"direct", "held"} {
			t.Run("NameFiltered/"+mode+"/"+route, func(t *testing.T) {
				e, s, peer, ps, keys := resetStatsFixture(t)
				a, b := keys[0], keys[2]
				// Both saved facts are positive and have exact, distinct reset times.
				e.Statistics.ResetSubscriptionOID(a.Object.Address.ObjectID, time.Unix(100, 123456000).UTC())
				peer.Statistics.ResetSubscriptionOID(b.Object.Address.ObjectID, time.Unix(200, 234567000).UTC())
				local := subscriptionStatsSeed(e, a, subscriptionStatsLocal)
				remote := subscriptionStatsSeed(peer, b, subscriptionStatsPeer)
				statsFunctionExec(t, s, "SET stats_fetch_consistency="+mode, "BEGIN")
				defer statsFunctionExec(t, s, "ROLLBACK")
				const query = "SELECT * FROM pg_stat_subscription_stats WHERE subname=$1"
				ast, description := enginetest.PreparedDescription(t, e, s, query)
				reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
				if !reader.IsZero() {
					t.Fatal("name description collected history")
				}
				var expected *core.Description
				if route == "held" {
					expected = &description
				}
				read := func(name string, key statistics.Key, want statistics.Fact) {
					t.Helper()
					for range 2 {
						result, err := subscriptionStatsNameExecute(t, e, s, ast, expected, &name)
						if err != nil {
							t.Fatal(err)
						}
						subscriptionStatsOwned(t, result, map[ids.OID]statistics.Fact{key.Object.Address.ObjectID: want})
					}
				}
				read("reset_target_a", a, local)
				cache := reader.FieldByName("cache")
				backing := cache.UnsafePointer()
				assertFact := func(key statistics.Key, want statistics.Fact, present bool) {
					t.Helper()
					got := cache.MapIndex(reflect.ValueOf(key))
					if got.IsValid() != present || present && (!got.FieldByName("exists").Bool() || !got.FieldByName("fact").Equal(reflect.ValueOf(want))) {
						t.Fatal("genuine qualified cached fact", key, got, want, present)
					}
				}
				assertFact(a, local, mode != "none")
				assertFact(b, remote, mode == "snapshot")
				assertFact(keys[1], e.Statistics.Read(keys[1]), mode == "snapshot")
				projected := b
				projected.Object = e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), b.Object.Address)
				assertFact(projected, remote, false)
				if reader.FieldByName("full").Bool() != (mode == "snapshot") || reader.FieldByName("stamp").IsZero() != (mode != "snapshot") {
					t.Fatal("name filtering changed full snapshot policy", mode)
				}
				// Peer reset diverges B before its first demanded read.
				resetStatsExec(t, ps, fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)", b.Object.Address.ObjectID))
				currentB := peer.Statistics.Read(b)
				if currentB.ResetAt.IsZero() || currentB == remote {
					t.Fatal("peer B reset did not distinguish retained history")
				}
				wantB := currentB
				if mode == "snapshot" {
					wantB = remote
				}
				read("reset_target_c", b, wantB)
				assertFact(b, wantB, mode != "none")
				resetStatsExec(t, ps, fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)", a.Object.Address.ObjectID))
				currentA := e.Statistics.Read(a)
				if currentA.ResetAt.IsZero() || currentA == local {
					t.Fatal("peer A reset did not distinguish retained positive history")
				}
				wantA := local
				if mode == "none" {
					wantA = currentA
				}
				// One retained description reselects A -> B -> A -> B, never A's old frame.
				read("reset_target_a", a, wantA)
				read("reset_target_c", b, wantB)
				assertFact(a, wantA, mode != "none")
				assertFact(b, wantB, mode != "none")
				if cache.UnsafePointer() != backing {
					t.Fatal("name reselection replaced actual retained cache backing")
				}
				before := bgwriterReaderState(s)
				unknown := "stats_name_absent"
				result, err := subscriptionStatsNameExecute(t, e, s, ast, expected, &unknown)
				if err != nil {
					t.Fatal(err)
				}
				subscriptionStatsOwned(t, result, nil)
				if bgwriterReaderState(s) != before {
					t.Fatal("nonmatching name supplemented or cleared retained history")
				}
				resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
				if cache.Len() != 0 || reader.FieldByName("full").Bool() || !reader.FieldByName("stamp").IsZero() {
					t.Fatal("clear retained name-filtered cut")
				}
				read("reset_target_a", a, currentA)
				read("reset_target_c", b, currentB)
			})
		}
	}
	for _, first := range []string{"table", "sequence", "index", "bgwriter", "archiver", "slru", "subscription", "database", "runtime", "runtime_database", "backend_io", "backend_wal"} {
		t.Run(first, func(t *testing.T) {
			e, s, peer, ps, keys := resetStatsFixture(t)
			a, c := keys[0], keys[2]
			local := subscriptionStatsSeed(e, a, subscriptionStatsLocal)
			remote := subscriptionStatsSeed(peer, c, subscriptionStatsPeer)
			statsFunctionExec(t, s, "CREATE TABLE stats_first(v integer)", "CREATE INDEX stats_first_idx ON stats_first USING hash(v)", "CREATE SEQUENCE stats_first_seq", "SET stats_fetch_consistency=snapshot", "BEGIN")
			// Peer relation/function facts stay excluded; owning backend and database facts share the cut.
			foreignRelation := statistics.Key{Object: peer.Catalog.Domain.Ref(peer.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 700001}), Category: statistics.Relation}
			foreignFunction := foreignRelation
			foreignFunction.Category = statistics.Function
			foreignDatabase := statistics.Key{Object: peer.Catalog.Domain.Ref(0, peer.Catalog.DatabaseAddress()), Category: statistics.Database}
			var writer statistics.Writer
			writer.Add(&peer.Statistics, foreignRelation, statistics.Fact{Inserts: 211})
			writer.Add(&peer.Statistics, foreignFunction, statistics.Fact{Calls: 223})
			writer.Add(&peer.Statistics, foreignDatabase, statistics.Fact{Database: statistics.DatabaseFact{Inserts: 233}})
			writer.Backend(&peer.Statistics, ps.Token(), statistics.Fact{Operations: 227}, statistics.Fact{Operations: 229})
			writer.Flush(&peer.Statistics, time.Now(), true)
			queries := map[string]string{"table": "SELECT seq_scan FROM pg_stat_all_tables WHERE relid='stats_first'::regclass", "sequence": "SELECT blks_read FROM pg_statio_all_sequences WHERE relid='stats_first_seq'::regclass", "index": "SELECT idx_scan FROM pg_stat_all_indexes WHERE indexrelid='stats_first_idx'::regclass", "bgwriter": "SELECT buffers_clean FROM pg_stat_bgwriter", "archiver": "SELECT archived_count FROM pg_stat_archiver", "slru": "SELECT blks_hit FROM pg_stat_slru"}
			queries["database"] = "SELECT tup_inserted FROM pg_stat_database"
			queries["backend_io"] = fmt.Sprintf("SELECT reads FROM pg_stat_get_backend_io(%d)", ps.Token().ID())
			queries["backend_wal"] = fmt.Sprintf("SELECT wal_records FROM pg_stat_get_backend_wal(%d)", ps.Token().ID())
			if first == "subscription" {
				subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), map[ids.OID]statistics.Fact{a.Object.Address.ObjectID: local, keys[1].Object.Address.ObjectID: e.Statistics.Read(keys[1]), c.Object.Address.ObjectID: remote})
			} else if first == "runtime" {
				read := e.CatalogRead()
				var ref ids.ObjectRef
				for _, descriptor := range read.Descriptors() {
					if descriptor.Address().ClassID == ids.RelationClassID && descriptor.Name() == "stats_first" {
						ref = read.Owner().Domain.Ref(read.Epoch(), descriptor.Address())
						break
					}
				}
				if ref == (ids.ObjectRef{}) {
					t.Fatal("missing actual runtime relation stats_first")
				}
				sharedStatsCapture(t, e, s, ref)
			} else if first == "runtime_database" {
				op, err := s.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				e.Mu.Unlock()
				if err != nil {
					scope.Finish(err)
					op.Done()
					t.Fatal(err)
				}
				runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
				got := runtime.DatabaseStatistics(peer.Catalog.DatabaseAddress().ObjectID).Cumulative.Inserts
				err = scope.Finish(nil)
				op.Done()
				if err != nil || got != 233 {
					t.Fatal("standalone database runtime lost peer facts", got, err)
				}
			} else {
				statsFunctionExec(t, s, queries[first])
			}
			owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
			cache := owned.FieldByName("cache")
			if !owned.FieldByName("full").Bool() || owned.FieldByName("stamp").IsZero() {
				t.Fatal("first cumulative source did not establish full cut", first)
			}
			for key, n := range map[statistics.Key]int64{a: 11, c: 101} {
				fact := cache.MapIndex(reflect.ValueOf(key))
				if !fact.IsValid() || !fact.FieldByName("exists").Bool() || fact.FieldByName("fact").FieldByName("ApplyErrorCount").Int() != n {
					t.Fatal("initial cut omitted genuine owning peer key", first, key)
				}
			}
			databaseFact := cache.MapIndex(reflect.ValueOf(foreignDatabase))
			if !databaseFact.IsValid() || !databaseFact.FieldByName("exists").Bool() || databaseFact.FieldByName("fact").FieldByName("Database").FieldByName("Inserts").Int() != 233 {
				t.Fatal("unified cut omitted genuine peer database key", first)
			}
			projected := c
			projected.Object = e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), c.Object.Address)
			for _, key := range []statistics.Key{projected, foreignRelation, foreignFunction} {
				if cache.MapIndex(reflect.ValueOf(key)).IsValid() {
					t.Fatal("imported projected or unrelated peer partition", key)
				}
			}
			if backend := owned.FieldByName("backends").MapIndex(reflect.ValueOf(ps.Token())); !backend.IsValid() || !backend.FieldByName("exists").Bool() || backend.FieldByName("fact").FieldByName("IO").FieldByName("Operations").Int() != 227 || backend.FieldByName("fact").FieldByName("WAL").FieldByName("Operations").Int() != 229 {
				t.Fatal("owning peer backend omitted or overwritten by mirrored zero")
			}
			retained := bgwriterReaderState(s)
			resetStatsExec(t, ps, fmt.Sprintf("SELECT pg_stat_reset_subscription_stats(%d::oid)", c.Object.Address.ObjectID))
			if peer.Statistics.Read(c).ResetAt.IsZero() {
				t.Fatal("peer reset did not diverge")
			}
			want := map[ids.OID]statistics.Fact{a.Object.Address.ObjectID: local, keys[1].Object.Address.ObjectID: e.Statistics.Read(keys[1]), c.Object.Address.ObjectID: remote}
			subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), want)
			if bgwriterReaderState(s) != retained {
				t.Fatal("later peer read refreshed/replaced original backing", first)
			}
			resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
			if owned.FieldByName("full").Bool() || !owned.FieldByName("stamp").IsZero() || cache.Len() != 0 {
				t.Fatal("clear retained shared peer cut")
			}
			want[c.Object.Address.ObjectID] = peer.Statistics.Read(c)
			subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), want)
			statsFunctionExec(t, s, "COMMIT", "BEGIN")
			if owned.FieldByName("full").Bool() || cache.Len() != 0 {
				t.Fatal("terminal lifecycle retained cache")
			}
			subscriptionStatsOwned(t, subscriptionStatsCapture(t, e, s), want)
			statsFunctionExec(t, s, "SET LOCAL stats_fetch_consistency=none", "SET LOCAL stats_fetch_consistency=snapshot")
			statsFunctionExec(t, s, "SELECT pg_stat_get_snapshot_timestamp()")
			if owned.FieldByName("full").Bool() || cache.Len() != 0 {
				t.Fatal("effective generation did not invalidate all partitions")
			}
			statsFunctionExec(t, s, "ROLLBACK")
		})
	}
	t.Run("ClusterFence", func(t *testing.T) {
		e, _, peer, _, keys := resetStatsFixture(t)
		key := keys[2]
		before := subscriptionStatsSeed(peer, key, subscriptionStatsPeer)
		var reader statistics.Reader
		reader.Configure("snapshot", 1)
		started, done := make(chan struct{}), make(chan struct{})
		at := time.Unix(200, 0)
		e.Mu.Lock()
		go func() {
			close(started)
			e.Mu.Lock()
			peer.Statistics.ResetSubscriptionOID(key.Object.Address.ObjectID, at)
			e.Mu.Unlock()
			close(done)
		}()
		<-started
		reader.Capture([]statistics.CaptureSource{{Provider: &e.Statistics, All: true}, {Provider: &peer.Statistics, Database: statistics.Key{Object: peer.Catalog.Domain.Ref(0, peer.Catalog.DatabaseAddress()), Category: statistics.Database}}}, time.Unix(100, 0))
		e.Mu.Unlock()
		<-done
		if got := reader.Read(&peer.Statistics, key, time.Time{}); got != before {
			t.Fatal("reset crossed serialized initial cut", got, before)
		}
		if got := peer.Statistics.Read(key); !got.ResetAt.Equal(at) || got.ApplyErrorCount != 0 {
			t.Fatal("blocked reset did not complete", got)
		}
	})
	// Exact-key cache participation is independent of public view evaluation.
	t.Run("KeyModes", func(t *testing.T) {
		for _, mode := range []string{"none", "cache", "snapshot"} {
			e, _, peer, _, keys := resetStatsFixture(t)
			var reader statistics.Reader
			reader.Configure(mode, 1)
			a, c := keys[0], keys[2]
			before := reader.Read(&e.Statistics, a, time.Unix(100, 0))
			if mode == "snapshot" {
				reader.Clear()
				reader.Capture([]statistics.CaptureSource{{Provider: &e.Statistics, All: true}, {Provider: &peer.Statistics, Database: statistics.Key{Object: peer.Catalog.Domain.Ref(0, peer.Catalog.DatabaseAddress()), Category: statistics.Database}}}, time.Unix(100, 0))
				before = reader.Read(&e.Statistics, a, time.Time{})
			}
			if mode == "cache" && statsLifetimeSnapshotContains(&reader, c) {
				t.Fatal("unread genuine peer already cached")
			}
			if mode == "snapshot" && !statsLifetimeSnapshotContains(&reader, c) {
				t.Fatal("full cut omitted genuine peer before access")
			}
			at := time.Unix(200, 0)
			e.Statistics.ResetSubscriptionOID(a.Object.Address.ObjectID, at)
			peer.Statistics.ResetSubscriptionOID(c.Object.Address.ObjectID, at)
			got := reader.Read(&e.Statistics, a, time.Time{})
			if mode == "none" {
				if !got.ResetAt.Equal(at) {
					t.Fatal("none failed current read")
				}
			} else if got != before {
				t.Fatal("retained exact key refreshed")
			}
			peerGot := reader.Read(&peer.Statistics, c, time.Time{})
			if mode == "snapshot" {
				if !peerGot.ResetAt.IsZero() && peerGot.ResetAt.Equal(at) {
					t.Fatal("full peer refreshed")
				}
			} else if !peerGot.ResetAt.Equal(at) {
				t.Fatal("unaccessed exact key was captured early")
			}
			if mode != "none" && !statsLifetimeSnapshotContains(&reader, c) {
				t.Fatal("accessed genuine peer lacks retained key")
			}
			missing := c
			missing.Object.Address.ObjectID += 1000000
			if f := reader.Read(&peer.Statistics, missing, time.Time{}); !f.ResetAt.IsZero() || f.ApplyErrorCount != 0 {
				t.Fatal("missing per-entry defaults", f)
			}
			reader.Clear()
			if f := reader.Read(&peer.Statistics, c, time.Unix(300, 0)); !f.ResetAt.Equal(at) {
				t.Fatal("clear did not refresh peer")
			}
		}
	})
}
