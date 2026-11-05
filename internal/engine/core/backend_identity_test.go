package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestBackendIdentityRuntimeLifetime(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, activityFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/activityFirst=%t", mode, activityFirst), func(t *testing.T) {
				e, s, peer, ps, _, _ := sharedStatsFixture(t)
				statsFunctionExec(t, s, "SET stats_fetch_consistency="+mode, "BEGIN")
				birth, _ := e.Statistics.LiveActivity(ps.Token())
				intType := enginetest.Usage(e, sqltype.Int4).Type
				startType := enginetest.Usage(e, sqltype.Timestamptz).Type
				var old sqltype.Value
				backendCurrentBorrow(t, e, s, func(_ *core.StatementScope, runtime exec.SessionInquiryRuntime) {
					if activityFirst {
						if _, err := runtime.BackendActivity(sqltype.Null(intType), backendCurrentShape(t, e, 2022)); err != nil {
							t.Fatal(err)
						}
					} else if _, err := runtime.BackendIdentityIDs(intType); err != nil {
						t.Fatal(err)
					}
					var err error
					old, err = runtime.BackendIdentityValue(functions.BackendStart, int64(birth.BackendID), startType)
					if got, failure := old.TimestampTime(); err != nil || failure != nil || !got.Equal(birth.BackendStart) {
						t.Fatal("actual retained physical birth", old, err, failure)
					}
				})
				reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
				if reader.FieldByName("activity").IsNil() || !reader.FieldByName("stamp").IsZero() {
					t.Fatal("activity capture initialized cumulative state")
				}
				oldToken := ps.Token()
				statsFunctionRetire(t, ps)
				replacement, err := enginetest.OpenSession(context.Background(), peer, session.SessionStart{ID: oldToken.ID() + 1000})
				if err != nil {
					t.Fatal(err)
				}
				defer statsFunctionRetire(t, replacement)
				current, _ := e.Statistics.LiveActivity(replacement.Token())
				if current.BackendID != birth.BackendID || current.Incarnation == birth.Incarnation {
					t.Fatal("actual new birth did not reuse retired slot", birth, current)
				}
				peer.Statistics.Reset(time.Now())
				peer.Statistics.Replace(time.Now(), nil)
				backendCurrentBorrow(t, e, s, func(_ *core.StatementScope, runtime exec.SessionInquiryRuntime) {
					v, err := runtime.BackendIdentityValue(functions.BackendPID, int64(birth.BackendID), intType)
					if n, failure := v.Integer(); err != nil || failure != nil || n != int64(oldToken.ID()) {
						t.Fatal("retained slot joined through live registry", n, err, failure)
					}
					v, err = runtime.BackendIdentityValue(functions.BackendStart, int64(birth.BackendID), startType)
					if err != nil || !reflect.DeepEqual(v, old) {
						t.Fatal("retirement/reset changed detached scalar", v, old, err)
					}
				})
				statsFunctionExec(t, s, "SELECT pg_stat_clear_snapshot() IS NULL")
				backendCurrentBorrow(t, e, s, func(_ *core.StatementScope, runtime exec.SessionInquiryRuntime) {
					v, err := runtime.BackendIdentityValue(functions.BackendPID, int64(birth.BackendID), intType)
					if n, failure := v.Integer(); err != nil || failure != nil || n != int64(replacement.Token().ID()) {
						t.Fatal("clear did not adopt genuine replacement", n, err, failure)
					}
				})
				statsFunctionExec(t, s, "ROLLBACK")
				if got, err := old.TimestampTime(); err != nil || !got.Equal(birth.BackendStart) {
					t.Fatal("clear/end mutated returned datum", got, err)
				}
			})
		}
	}
	t.Run("accepted_prepared_ownership", identityPreparedOwnership)
}

func identityPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("identity_owned", preparedFoldPoint+",pg_stat_get_backend_start(0) AS live", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("identity_owned")
	owner := definition.Description.Folds
	if owner == nil || !owner.Live() || owner.Current().Generation != 0 {
		t.Fatal("Parse lost actual definition owner")
	}
	reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
	if !reader.FieldByName("activity").IsNil() || !reader.FieldByName("stamp").IsZero() {
		t.Fatal("Parse captured monitoring state")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "identity_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("identity_first")
	first := registry.Binding("identity_first")
	accepted := owner.Current()
	entries := append([]exec.FoldConstant(nil), accepted.Entries...)
	if len(entries) == 0 || first == nil || first.Resource == nil || first.Resource.Root == nil {
		t.Fatal("missing actual accepted graph and fold backing")
	}
	root := first.Resource.Root
	analysis := definition.Description.Analysis
	call, ok := root.Expressions[1].(functions.BackendIdentity)
	if !ok || call.Kind != functions.BackendStart || call.RoutineReference == nil || call.RoutineReference.RoutineOID() != 1391 || call.Argument == nil {
		t.Fatal("typed runtime recipe erased or relabeled", root.Expressions[1])
	}
	if !reader.FieldByName("activity").IsNil() || !reader.FieldByName("stamp").IsZero() {
		t.Fatal("Bind captured monitoring state")
	}
	assertUnchanged := func() {
		t.Helper()
		current := owner.Current()
		if definition.Description.Folds != owner || definition.Description.Analysis != analysis || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(current, accepted) || first.Resource.Root != root || !reflect.DeepEqual(root.Expressions[1], call) {
			t.Fatal("accepted owner/generation/backing/recipe changed")
		}
		for i, entry := range entries {
			if !enginetest.SamePublishedFold(entry, current.Entries[i]) {
				t.Fatal("accepted immutable fold payload mutated", i)
			}
		}
	}
	bind("identity_second")
	second := registry.Binding("identity_second")
	if second.Resource.Root == root || second.Entry.Description.Folds != owner || &second.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("portal graph did not share original definition backing")
	}
	for _, name := range []string{"identity_first", "identity_second"} {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		assertUnchanged()
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var env core.PreparedEnvironment
	if err == nil {
		env, err = e.PreparedEnvironmentLocked(auth, true)
	}
	var publication *core.FoldPublication
	if err == nil {
		publication, err = core.BeginFoldPublication(&definition.Description, definition.Body, env, auth.Catalog, nil)
	}
	if err == nil && publication != nil {
		publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(ctx, auth.Catalog, env, e.Catalog.State.Epoch())
	}
	e.Mu.Unlock()
	op.Done()
	if publication == nil || !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("canceled candidate publication accepted", err)
	}
	assertUnchanged()
	for _, name := range []string{"identity_first", "identity_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("identity_owned") })
	op, _, err = s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

func TestBackendIdentityConcurrentCapture(t *testing.T) {
	e := engine.New()
	observer := enginetest.StartSession(t, e, nil)
	intType := enginetest.Usage(e, sqltype.Int4).Type
	requests, results := make(chan struct{}), make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-requests:
			}
			peer, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: enginetest.NextSessionID()})
			if err == nil {
				e.Mu.Lock()
				e.Statistics.Reset(time.Now())
				e.Statistics.Replace(time.Now(), nil)
				e.Mu.Unlock()
				peer.RequestEnd(session.CleanClose)
				err = peer.WaitEnd(context.Background())
			}
			select {
			case results <- err:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-done }()
	for range 32 {
		select {
		case requests <- struct{}{}:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		backendCurrentBorrow(t, e, observer, func(scope *core.StatementScope, runtime exec.SessionInquiryRuntime) {
			if _, err := core.HostOf(scope).StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
				t.Fatal(err)
			}
			values, err := runtime.BackendIdentityIDs(intType)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[int64]bool{}
			for _, value := range values {
				id, err := value.Integer()
				if err != nil || id < 0 || seen[id] {
					t.Fatal("torn/duplicate live slot cut", values, err)
				}
				seen[id] = true
			}
		})
		select {
		case err := <-results:
			if err != nil {
				t.Fatal("birth/reset/retirement worker", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	cancel()
	<-done
	backendCurrentBorrow(t, e, observer, func(scope *core.StatementScope, runtime exec.SessionInquiryRuntime) {
		if _, err := core.HostOf(scope).StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
			t.Fatal(err)
		}
		values, err := runtime.BackendIdentityIDs(intType)
		if err != nil || len(values) != 1 {
			t.Fatal("joined workers left public activity", values, err)
		}
	})
}
