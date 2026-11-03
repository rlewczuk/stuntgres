package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatBGWriterClusterAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	ps := enginetest.StartSession(t, e, nil)
	started := time.UnixMicro(e.Cluster.StartedAt + 946684800000000).UTC()
	if !e.Cluster.BGWriterResetAt.Equal(started) || !e.Statistics.ReadShared(statistics.BGWriter).ResetAt.Equal(started) {
		t.Fatal("cluster initialization must convert PostgreSQL epoch", started, e.Cluster.BGWriterResetAt)
	}
	// Interpose only the real private seed factory. Reset while CREATE DATABASE
	// is outside the cluster mutex, then let genuine preparation/publication run.
	factory := e.Factory
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.Factory = func(definitions []settings.Definition, database string, track bool) (*core.Engine, error) {
		close(entered)
		<-release
		return factory(definitions, database, track)
	}
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	defer cancel()
	done := enginetest.LockAsync(e, s, ctx, "CREATE DATABASE bgwriter_racing_target", false)
	joined := false
	defer func() {
		once.Do(func() { close(release) })
		if !joined {
			enginetest.LockAwait(t, done, nil)
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("CREATE never entered real seed preparation", ctx.Err())
	}
	resetStatsExec(t, ps, "SELECT pg_stat_reset_shared('bgwriter')")
	at := e.Cluster.BGWriterResetAt
	if !at.After(started) {
		t.Fatal("reset must distinguish startup authority")
	}
	once.Do(func() { close(release) })
	enginetest.LockAwait(t, done, nil)
	joined = true
	e.Factory = factory
	peer, err := e.SelectDatabase(t.Context(), "bgwriter_racing_target")
	if err != nil {
		t.Fatal(err)
	}
	if !peer.Statistics.ReadShared(statistics.BGWriter).ResetAt.Equal(at) || peer.Cluster != e.Cluster {
		t.Fatal("late publication used stale staging/template reset")
	}
	peerSession := enginetest.StartSession(t, peer, nil)
	resetStatsExec(t, peerSession, "SELECT pg_stat_reset_shared('bgwriter')")
	at = e.Cluster.BGWriterResetAt
	for _, target := range []*core.Engine{e, peer} {
		if !target.Statistics.ReadShared(statistics.BGWriter).ResetAt.Equal(at) {
			t.Fatal("registered provider mirror diverged")
		}
	}
	for _, query := range []string{"SELECT pg_stat_reset_shared('wal')", "SELECT pg_stat_reset_shared('archiver')", "SELECT pg_stat_reset_slru(NULL)"} {
		resetStatsExec(t, ps, query)
		if !e.Cluster.BGWriterResetAt.Equal(at) {
			t.Fatal("unrelated reset changed cluster authority", query)
		}
	}
	// The strict NULL backend reset returns a typed NULL, not the non-NULL
	// VOID datum produced by shared/SLRU resets, and changes no authority.
	beforeNull := [2]statistics.SharedFact{e.Statistics.ReadShared(statistics.BGWriter), peer.Statistics.ReadShared(statistics.BGWriter)}
	func() {
		op, err := ps.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		result, err := e.Execute(op, enginetest.Statement(t, "SELECT pg_stat_reset_backend_stats(NULL)"), 0, nil)
		if result.Rows != nil {
			defer result.Rows.Close()
		}
		if err != nil || result.Rows == nil {
			t.Fatal("strict NULL backend reset cursor", err)
		}
		columns := result.Rows.Schema()
		if len(columns) != 1 || columns[0].Usage.Type == nil || columns[0].Usage.Type.ID() != sqltype.Void {
			t.Fatal("strict NULL backend reset VOID schema", columns)
		}
		var values [1]sqltype.Value
		if present, err := result.Rows.Next(op.Context(), values[:]); err != nil || !present {
			t.Fatal("strict NULL backend reset singleton", present, err)
		}
		if !values[0].IsNull() || values[0].Type() != columns[0].Usage.Type {
			t.Fatal("strict NULL backend reset must return typed VOID NULL", values[0])
		}
		if present, err := result.Rows.Next(op.Context(), values[:]); err != nil || present {
			t.Fatal("strict NULL backend reset extra row", present, err)
		}
	}()
	if !e.Cluster.BGWriterResetAt.Equal(at) {
		t.Fatal("strict NULL backend reset changed cluster authority")
	}
	for i, target := range []*core.Engine{e, peer} {
		if target.Statistics.ReadShared(statistics.BGWriter) != beforeNull[i] {
			t.Fatal("strict NULL backend reset changed bgwriter provider mirror")
		}
	}
	if err := resetStatsSQL(t, ps, "SELECT pg_stat_reset_shared('WAL')"); err == nil || !e.Cluster.BGWriterResetAt.Equal(at) {
		t.Fatal("invalid reset changed authority", err)
	}
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	for _, target := range []*core.Engine{e, peer} {
		if !target.Statistics.ReadShared(statistics.BGWriter).ResetAt.Equal(at) {
			t.Fatal("local replacement lost shared authority")
		}
	}
	resetStatsExec(t, ps, "CREATE ROLE bgwriter_reset_denied", "SET ROLE bgwriter_reset_denied")
	if err := resetStatsSQL(t, ps, "SELECT pg_stat_reset_shared('bgwriter')"); err == nil || !e.Cluster.BGWriterResetAt.Equal(at) {
		t.Fatal("denied reset changed authority", err)
	}
	resetStatsExec(t, ps, "RESET ROLE")
	// The successful racing creation already consumed this cluster's SeedOnce.
	// Exercise failed/canceled preparation on fresh real owners so each injected
	// factory is actually reached, rather than bypassed by the cached seed.
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed_preparation", true: "canceled_preparation"}[interrupted], func(t *testing.T) {
			target := engine.New()
			session := enginetest.StartSession(t, target, nil)
			resetStatsExec(t, session, "SELECT pg_stat_reset_shared('bgwriter')")
			var writer statistics.Writer
			writer.Shared(&target.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [8]int64{3, 5, 7}})
			writer.Flush(&target.Statistics, time.Now(), true)
			before := target.Statistics.ReadShared(statistics.BGWriter)
			authority := target.Cluster.BGWriterResetAt
			targets, nextOID := target.Cluster.Targets, target.Cluster.NextOID
			prepare := target.Factory
			defer func() { target.Factory = prepare }()
			operationContext, stop := context.WithCancel(t.Context())
			defer stop()
			failed := errors.New("bgwriter preparation failed")
			want := error(failed)
			if interrupted {
				want = context.Canceled
			}
			entered := false
			target.Factory = func(definitions []settings.Definition, database string, track bool) (*core.Engine, error) {
				entered = true
				if !interrupted {
					return nil, failed
				}
				seed, err := prepare(definitions, database, track)
				if err == nil {
					stop()
				}
				return seed, err
			}
			op, err := session.Acquire(operationContext)
			if err != nil {
				t.Fatal(err)
			}
			result, err := target.Execute(op, enginetest.Statement(t, "CREATE DATABASE bgwriter_rejected_target"), 0, nil)
			op.Done()
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("rejected database creation returned rows")
			}
			if !entered || !errors.Is(err, want) {
				t.Fatal("real preparation boundary was not rejected", entered, err, want)
			}
			if len(target.Cluster.Targets) != len(targets) || target.Cluster.NextOID != nextOID || !target.Cluster.BGWriterResetAt.Equal(authority) {
				t.Fatal("rejected preparation changed registered authority or allocator")
			}
			for name, existing := range targets {
				if target.Cluster.Targets[name] != existing || existing.Statistics.ReadShared(statistics.BGWriter) != before {
					t.Fatal("rejected preparation changed registered runtime or positive facts", name)
				}
			}
			if target.Cluster.Targets["bgwriter_rejected_target"] != nil {
				t.Fatal("rejected preparation published a database")
			}
		})
	}
}
