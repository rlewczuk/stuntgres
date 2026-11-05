package core_test

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func backendCurrentShape(t *testing.T, e *core.Engine, oid ids.OID) *sqltype.Descriptor {
	t.Helper()
	view := e.CatalogRead()
	out := view.MustProcedure(ids.ObjectAddress{ClassID: 1255, ObjectID: oid}).OutputDeclaration()
	members := make([]sqltype.CompositeMember, out.Len())
	for i := range members {
		typ, _ := view.LookupType(sqltype.ID(out.Type(i)))
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: out.Name(i + out.Inputs()), Usage: typ.Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}
func backendCurrentBorrow(t *testing.T, e *core.Engine, s *session.Session, fn func(*core.StatementScope, exec.SessionInquiryRuntime)) {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	fn(scope, core.HostOf(scope).SessionInquiry())
}

func TestBackendCurrentConcurrentLifetime(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []string{"activity", "cumulative"} {
			t.Run(mode+"/"+first, func(t *testing.T) {
				e, s, peer, ps, _, _ := sharedStatsFixture(t)
				statsFunctionExec(t, s, "SET stats_fetch_consistency="+mode, "BEGIN")
				sharedStatsBackend(peer, ps, 17, 19)
				at := time.Unix(200, 0)
				peer.Statistics.ResetBackend(ps.Token(), at)
				birth, _ := e.Statistics.LiveActivity(ps.Token())
				if birth.Incarnation == 0 {
					t.Fatal("actual physical birth missing")
				}
				walShape := backendCurrentShape(t, e, 6313)
				activityShape := backendCurrentShape(t, e, 2022)
				var old sqltype.Value
				backendCurrentBorrow(t, e, s, func(scope *core.StatementScope, runtime exec.SessionInquiryRuntime) {
					if first == "activity" {
						argType, _ := e.CatalogRead().LookupType(sqltype.Int4)
						if _, err := runtime.BackendActivity(sqltype.Null(argType), activityShape); err != nil {
							t.Fatal(err)
						}
						var err error
						old, err = runtime.BackendWAL(int64(ps.Token().ID()), walShape)
						if err != nil || old.IsNull() {
							t.Fatal("live backend whole datum", old, err)
						}
					} else {
						e.Mu.Lock()
						auth, err := e.AuthorizationLocked(scope.Op)
						e.Mu.Unlock()
						if err != nil {
							t.Fatal(err)
						}
						r := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
						r.DatabaseStatistics(peer.Catalog.DatabaseAddress().ObjectID)
					}
				})
				var late statistics.Writer
				late.Backend(&peer.Statistics, ps.Token(), statistics.Fact{Operations: 101}, statistics.Fact{Operations: 103})
				token := ps.Token()
				statsFunctionRetire(t, ps)
				replacement, err := enginetest.OpenSession(context.Background(), peer, session.SessionStart{ID: token.ID()})
				if err != nil {
					t.Fatal(err)
				}
				defer statsFunctionRetire(t, replacement)
				if replacement.Token() != token {
					t.Fatal("fixture did not exercise genuine same-token reuse")
				}
				current, _ := e.Statistics.LiveActivity(token)
				if current.Incarnation == birth.Incarnation {
					t.Fatal("physical birth stamp recycled")
				}
				late.Flush(&peer.Statistics, time.Now(), true)
				if fact, _ := peer.Statistics.ReadBackend(token); fact != (statistics.BackendFact{}) {
					t.Fatal("retired pending adopted replacement", fact)
				}
				backendCurrentBorrow(t, e, s, func(_ *core.StatementScope, runtime exec.SessionInquiryRuntime) {
					value, err := runtime.BackendWAL(int64(token.ID()), walShape)
					absent := first == "activity" || mode == "snapshot"
					if err != nil || value.IsNull() != absent {
						t.Fatal("retained cut adopted new incarnation", first, mode, value, err)
					}
					if !old.IsNull() && old.Type() != nil {
						stamp, err := old.CompositeField(4).TimestampTime()
						if err != nil || !stamp.Equal(at) {
							t.Fatal("retained datum mutated", stamp, err)
						}
					}
				})
				statsFunctionExec(t, s, "SELECT pg_stat_clear_snapshot() IS NULL")
				backendCurrentBorrow(t, e, s, func(_ *core.StatementScope, runtime exec.SessionInquiryRuntime) {
					value, err := runtime.BackendWAL(int64(token.ID()), walShape)
					if err != nil || value.IsNull() || !value.CompositeField(4).IsNull() {
						t.Fatal("clear failed to admit new logical zero", value, err)
					}
				})
				statsFunctionExec(t, s, "ROLLBACK")
			})
		}
	}
	t.Run("ResetCaptureFence", func(t *testing.T) {
		e, s, peer, ps, _, _ := sharedStatsFixture(t)
		sharedStatsBackend(peer, ps, 41, 43)
		birth, _ := e.Statistics.LiveActivity(ps.Token())
		identity := statistics.BackendIdentity{Token: ps.Token(), Incarnation: birth.Incarnation}
		var reader statistics.Reader
		reader.Configure("snapshot", 1)
		started, done := make(chan struct{}), make(chan struct{})
		e.Mu.Lock()
		go func() {
			close(started)
			e.Mu.Lock()
			peer.Statistics.ResetBackend(ps.Token(), time.Unix(400, 0))
			e.Mu.Unlock()
			close(done)
		}()
		<-started
		reader.Capture([]statistics.CaptureSource{{Provider: &e.Statistics, All: true}, {Provider: &peer.Statistics, Owners: []statistics.BackendIdentity{identity}}}, time.Now())
		e.Mu.Unlock()
		<-done
		fact, ok := reader.ReadBackend(&peer.Statistics, identity, time.Now())
		if !ok || fact.IO.Operations != 41 || fact.WAL.Operations != 43 {
			t.Fatal("reset crossed serialized owner cut", fact, ok)
		}
		statsFunctionExec(t, s, fmt.Sprintf("SELECT pg_stat_reset_backend_stats(%d) IS NULL", ps.Token().ID()))
	})
	t.Run("GenericOperationsAreNotPhysical", func(t *testing.T) {
		e, s, peer, ps, _, _ := sharedStatsFixture(t)
		sharedStatsBackend(peer, ps, 53, 59)
		walShape, ioShape := backendCurrentShape(t, e, 6313), backendCurrentShape(t, e, 6386)
		backendCurrentBorrow(t, e, s, func(_ *core.StatementScope, runtime exec.SessionInquiryRuntime) {
			value, err := runtime.BackendWAL(int64(ps.Token().ID()), walShape)
			if err != nil || value.IsNull() {
				t.Fatal(value, err)
			}
			for _, i := range []int{0, 1, 3} {
				if n, err := value.CompositeField(i).Integer(); err != nil || n != 0 {
					t.Fatal("generic WAL operations became physical", n, err)
				}
			}
			zero, err := sqltype.NumericUint64Value(walShape.CompositeMember(2).Usage.Type, 0)
			if err != nil || value.CompositeField(2) != zero || !value.CompositeField(4).IsNull() {
				t.Fatal("physical bytes/reset", value, err)
			}
			rows, err := runtime.BackendIO(int64(ps.Token().ID()), ioShape)
			if err != nil || len(rows) != 8 {
				t.Fatal(rows, err)
			}
			for _, row := range rows {
				for i := 3; i < 19; i++ {
					v := row.CompositeField(i)
					if !v.IsNull() && v.Type().ID() == sqltype.Int8 {
						if n, err := v.Integer(); err != nil || n != 0 {
							t.Fatal("generic IO operations became physical", i, n, err)
						}
					}
				}
			}
		})
		fact, _ := peer.Statistics.ReadBackend(ps.Token())
		if fact.IO.Operations != 53 || fact.WAL.Operations != 59 {
			t.Fatal("monitoring changed generic facts", fact)
		}
	})
}
