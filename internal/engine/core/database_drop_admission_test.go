package core_test

import (
	"context"
	"errors"
	"runtime"
	"stuntgres/internal/engine/lock"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

func databaseDropOwnerFixture(t *testing.T) (*core.Engine, *core.Engine, *session.Session) {
	t.Helper()
	e := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	if _, err := databaseDropOwnerExecute(t.Context(), e, admin, &parser.CreateDatabaseStmt{Name: "drop_owner_target"}); err != nil {
		t.Fatal(err)
	}
	target, err := e.SelectDatabase(t.Context(), "drop_owner_target")
	if err != nil {
		t.Fatal(err)
	}
	return e, target, admin
}

func databaseDropOwnerExecute(ctx context.Context, e *core.Engine, s *session.Session, statement parser.Statement) (core.Result, error) {
	op, err := s.Acquire(ctx)
	if err != nil {
		return core.Result{}, err
	}
	defer op.Done()
	return e.Execute(op, statement, 0, nil)
}

func databaseDropOwnerStatement(force bool) *parser.DropDatabaseStmt {
	statement := &parser.DropDatabaseStmt{Name: "drop_owner_target"}
	if force {
		statement.Options = []parser.DatabaseOption{{Name: "force"}}
	}
	return statement
}

func databaseDropReceive(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(enginetest.Liveness):
		t.Fatal("database DROP worker did not finish")
		return context.DeadlineExceeded
	}
}

type databaseDropDefaults struct {
	entered, canceled, release chan struct{}
	once                       sync.Once
	touched                    atomic.Bool
}

func (d *databaseDropDefaults) SessionDefault(ctx context.Context, definition settings.Definition) (string, bool, error) {
	d.touched.Store(true)
	if definition.Name != "application_name" {
		return "", false, nil
	}
	d.once.Do(func() { close(d.entered) })
	select {
	case <-ctx.Done():
		close(d.canceled)
		<-d.release
		return "", false, ctx.Err()
	case <-d.release:
		return "", false, nil
	}
}

func TestDatabaseDropAdmission(t *testing.T) {
	t.Run("all_reserved_principals_preflight_both_orders", databaseDropReservedPreflight)
	t.Run("reserved_single_use_and_late_start", func(t *testing.T) {
		e, target, admin := databaseDropOwnerFixture(t)
		principal := admin.CountedAuthenticated
		id := enginetest.NextSessionID()
		reservation, err := e.AdmitDatabase(t.Context(), principal, id, core.DatabaseSelector{Name: target.DatabaseName, Explicit: true})
		if err != nil {
			t.Fatal(err)
		}
		defer reservation.Release()
		copied := *reservation
		if s, err := target.StartSession(t.Context(), &copied, session.SessionStart{ID: id}); s != nil || err == nil {
			t.Fatal("copied admission acquired authority")
		}
		if got := core.EngineDatabaseAdmissionCount(target); got != 1 {
			t.Fatal("invalid claim changed occupancy", got)
		}
		if _, err := databaseDropOwnerExecute(t.Context(), e, admin, databaseDropOwnerStatement(true)); err != nil {
			t.Fatal(err)
		}
		touched := &databaseDropDefaults{}
		if s, err := target.StartSession(t.Context(), reservation, session.SessionStart{ID: id, Defaults: touched}); s != nil || err == nil {
			t.Fatal("revoked reservation started", s, err)
		}
		if touched.touched.Load() || core.EngineDatabaseAdmissionCount(target) != 0 || core.EngineRoleSessionCountLocked(e, principal) != 1 {
			t.Fatal("late callback or reservation leak")
		}
		if _, err := e.SelectDatabase(t.Context(), target.DatabaseName); err == nil {
			t.Fatal("retired target remains selectable")
		}
	})
	t.Run("initializing_callback_is_canceled_and_joined", func(t *testing.T) {
		e, target, admin := databaseDropOwnerFixture(t)
		ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
		defer cancel()
		id := enginetest.NextSessionID()
		reservation, err := e.AdmitDatabase(t.Context(), admin.CountedAuthenticated, id, core.DatabaseSelector{Name: target.DatabaseName, Explicit: true})
		if err != nil {
			t.Fatal(err)
		}
		defer reservation.Release()
		defaults := &databaseDropDefaults{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
		var release sync.Once
		unblock := func() { release.Do(func() { close(defaults.release) }) }
		startDone, dropDone := make(chan error, 1), make(chan error, 1)
		joinedStart, joinedDrop := false, true
		defer func() {
			unblock()
			cancel()
			if !joinedStart {
				<-startDone
			}
			if !joinedDrop {
				<-dropDone
			}
		}()
		go func() {
			s, err := target.StartSession(ctx, reservation, session.SessionStart{ID: id, Defaults: defaults})
			if s != nil {
				s.RequestEnd(session.CleanClose)
				_ = s.WaitEnd(context.Background())
			}
			startDone <- err
		}()
		select {
		case <-defaults.entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		joinedDrop = false
		go func() {
			_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(true))
			dropDone <- err
		}()
		select {
		case <-defaults.canceled:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		// The callback deliberately retains its real initialization lease. Metadata
		// remains available and logical disappearance must not publish early.
		e.Mu.Lock()
		present := e.Cluster.Targets[target.DatabaseName] == target
		e.Mu.Unlock()
		if !present || core.EngineDatabaseAdmissionCount(target) != 1 {
			t.Fatal("DROP bypassed initializer drain")
		}
		select {
		case err := <-dropDone:
			joinedDrop = true
			t.Fatal("DROP completed before callback drain", err)
		default:
		}
		unblock()
		startErr := databaseDropReceive(t, startDone)
		joinedStart = true
		if startErr == nil {
			t.Fatal("terminated initializer published")
		}
		dropErr := databaseDropReceive(t, dropDone)
		joinedDrop = true
		if dropErr != nil {
			t.Fatal(dropErr)
		}
		if core.EngineDatabaseAdmissionCount(target) != 0 {
			t.Fatal("initializer reservation survived")
		}
	})
	t.Run("ending_owner_holds_real_lease", func(t *testing.T) {
		e, target, admin := databaseDropOwnerFixture(t)
		victim := enginetest.StartSession(t, target, nil)
		lease, err := victim.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Done()
		victim.RequestEnd(session.CleanClose)
		done := make(chan error, 1)
		joined := false
		defer func() {
			lease.Done()
			if !joined {
				<-done
			}
		}()
		go func() {
			_, err := databaseDropOwnerExecute(t.Context(), e, admin, databaseDropOwnerStatement(true))
			done <- err
		}()
		// Wait for the issuer to reach its genuine completion wait, then release the
		// old physical lease. Reading metadata while retained detects lock inversion.
		<-victim.EndRequested()
		_ = target.CatalogRead()
		if core.EngineDatabaseAdmissionCount(target) != 1 {
			t.Fatal("ending owner no longer counted")
		}
		select {
		case err := <-done:
			joined = true
			t.Fatal("DROP did not join ending lease", err)
		default:
		}
		lease.Done()
		dropErr := databaseDropReceive(t, done)
		joined = true
		if dropErr != nil {
			t.Fatal(dropErr)
		}
		select {
		case <-victim.Completed:
		default:
			t.Fatal("DROP returned before canonical completion")
		}
	})
	t.Run("cancellation_before_signaling_preserves_roots", func(t *testing.T) {
		e, target, admin := databaseDropOwnerFixture(t)
		victim := enginetest.StartSession(t, target, nil)
		root, peer := target.CatalogRead().State(), e.CatalogRead().State()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(true)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if target.CatalogRead().State() != root || e.CatalogRead().State() != peer || core.EngineDatabaseAdmissionCount(target) != 1 {
			t.Fatal("canceled drop changed roots/owners")
		}
		if op, err := victim.Acquire(t.Context()); err != nil {
			t.Fatal("canceled drop terminated victim", err)
		} else {
			op.Done()
		}
	})
	t.Run("queued_startup_RX_cannot_adopt_recreation", func(t *testing.T) {
		e, target, admin := databaseDropOwnerFixture(t)
		victim := enginetest.StartSession(t, target, nil)
		ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
		defer cancel()
		lease, err := victim.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Done()
		dropDone, startDone := make(chan error, 1), make(chan error, 1)
		dropJoined, startJoined := false, true
		defer func() {
			lease.Done()
			cancel()
			if !dropJoined {
				<-dropDone
			}
			if !startJoined {
				<-startDone
			}
		}()
		go func() {
			_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(true))
			dropDone <- err
		}()
		select {
		case <-victim.EndRequested():
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		id := enginetest.NextSessionID()
		startJoined = false
		go func() {
			admission, err := e.AdmitDatabase(ctx, admin.CountedAuthenticated, id, core.DatabaseSelector{Name: target.DatabaseName, Explicit: true})
			if admission != nil {
				admission.Release()
				if err == nil {
					err = errors.New("queued startup acquired a dropped incarnation")
				}
			}
			startDone <- err
		}()
		for {
			records, err := e.Locks.PGLocks(e.Catalog.Domain, e.CatalogRead().Epoch())
			if err != nil {
				t.Fatal(err)
			}
			pending := false
			for _, record := range records {
				if record.Waiting && record.Kind == lock.ObjectRecord && record.Owner.Session == id && record.Mode == lock.RowExclusive {
					pending = true
				}
			}
			if pending {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("startup never queued behind DROP AX", ctx.Err())
			default:
				runtime.Gosched()
			}
		}
		if core.EngineDatabaseAdmissionCount(target) != 1 {
			t.Fatal("queued startup entered reservation cohort early")
		}
		lease.Done()
		dropErr := databaseDropReceive(t, dropDone)
		dropJoined = true
		if dropErr != nil {
			t.Fatal(dropErr)
		}
		if _, err := databaseDropOwnerExecute(ctx, e, admin, &parser.CreateDatabaseStmt{Name: target.DatabaseName}); err != nil {
			t.Fatal(err)
		}
		startErr := databaseDropReceive(t, startDone)
		startJoined = true
		enginetest.State(t, startErr, "3D000")
	})
}
