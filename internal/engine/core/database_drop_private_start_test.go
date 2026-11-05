package core_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestDatabaseDropPrivateInitialization(t *testing.T) {
	e, target, admin := databaseDropOwnerFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	defer cancel()
	defaults := &databaseDropDefaults{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(defaults.release) }) }
	startDone, dropDone, progressDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	startJoined, dropStarted, dropJoined, progressStarted, progressJoined := false, false, false, false, false
	defer func() {
		unblock()
		cancel()
		if !startJoined {
			<-startDone
		}
		if dropStarted && !dropJoined {
			<-dropDone
		}
		if progressStarted && !progressJoined {
			<-progressDone
		}
	}()
	// This is the actual private initializer used by StartRestoreSession, with
	// a genuine Settings resource callback. It is not the outer trusted replay
	// origin association, and no public reservation or fake owner is installed.
	go func() {
		s, err := core.EngineStartPrivateSession(target, ctx, admin.CountedAuthenticated, defaults)
		if s != nil {
			s.RequestEnd(session.CleanClose)
			_ = s.WaitEnd(context.Background())
		}
		startDone <- err
	}()
	select {
	case <-defaults.entered:
	case err := <-startDone:
		startJoined = true
		t.Fatal("private initializer did not reach callback", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	starts := core.EnginePrivateStarts(target)
	if len(starts) != 1 {
		t.Fatal("initializing owner absent from actual privateStarts", starts)
	}
	owner := starts[0]
	owner.Mu.Lock()
	initializing := owner.State == session.Initializing && owner.Leases == 1 && owner.Settings != nil && owner.Authorization != nil
	owner.Mu.Unlock()
	core.EngineSessionMuRef(target).Lock()
	published := len(core.EngineSessions(target))
	core.EngineSessionMuRef(target).Unlock()
	if !initializing || owner.SessionToken.ID() != 0 || owner.SessionToken.Private() != owner || published != 0 {
		t.Fatal("fixture substituted a live or fabricated private owner")
	}
	if core.EngineDatabaseAdmissionCount(target) != 0 || core.EngineRoleSessionCountLocked(e, admin.CountedAuthenticated) != 1 || len(e.Statistics.CaptureActivities()) != 1 {
		t.Fatal("private initializer fabricated public occupancy/activity")
	}
	// Start the independent physical peer before DROP; creating it during the
	// drain would itself block synchronously if DROP wrongly retained metadata.
	peer := enginetest.StartSession(t, e, nil)
	peerStatement := enginetest.Statement(t, "SELECT 1")
	endRequested := owner.EndRequested()
	dropStarted = true
	go func() {
		_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(true))
		dropDone <- err
	}()
	select {
	case <-defaults.canceled:
	case <-ctx.Done():
		t.Fatal("DROP did not discover/cancel privateStarts owner", ctx.Err())
	}
	select {
	case <-endRequested:
	default:
		t.Fatal("callback cancellation lacked genuine owner termination")
	}
	select {
	case <-owner.Completed:
		t.Fatal("private completion bypassed callback lease")
	default:
	}
	select {
	case err := <-dropDone:
		dropJoined = true
		t.Fatal("DROP did not join private initialization", err)
	default:
	}
	// Probe in a joined worker: an incorrect join under Mu or ReplacementMu
	// must reach the bounded failure path, which releases the callback and
	// cancels DROP before waiting for any blocked probe.
	progressStarted = true
	go func() {
		result, err := databaseDropOwnerExecute(ctx, e, peer, peerStatement)
		if result.Rows != nil {
			closeErr := result.Rows.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			progressDone <- fmt.Errorf("peer progress blocked by private drain: %w", err)
			return
		}
		target.ReplacementMu.Lock()
		target.ReplacementMu.Unlock()
		e.Mu.Lock()
		present := e.Cluster.Targets[target.DatabaseName] == target
		e.Mu.Unlock()
		if !present || len(core.EnginePrivateStarts(target)) != 1 {
			progressDone <- fmt.Errorf("DROP published before genuine private drain")
			return
		}
		s, err := target.StartRestoreSession(ctx, admin.CountedAuthenticated)
		if s != nil {
			s.RequestEnd(session.CleanClose)
			_ = s.WaitEnd(context.Background())
		}
		if s != nil || err == nil {
			progressDone <- fmt.Errorf("unrelated private start entered draining database: %v", err)
			return
		}
		progressDone <- nil
	}()
	select {
	case err := <-progressDone:
		progressJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("peer/lock progress blocked by private drain", ctx.Err())
	}
	unblock()
	startErr := databaseDropReceive(t, startDone)
	startJoined = true
	if startErr == nil {
		t.Fatal("canceled private initializer published live")
	}
	dropErr := databaseDropReceive(t, dropDone)
	dropJoined = true
	if dropErr != nil {
		t.Fatal(dropErr)
	}
	select {
	case <-owner.Completed:
	default:
		t.Fatal("retirement preceded actual private completion")
	}
	owner.Mu.Lock()
	clean := owner.State == session.Ended && owner.Leases == 0 && len(owner.Operations) == 0
	for _, resource := range owner.Resources {
		clean = clean && resource == nil
	}
	owner.Mu.Unlock()
	core.EngineSessionMuRef(target).Lock()
	published = len(core.EngineSessions(target))
	core.EngineSessionMuRef(target).Unlock()
	if !clean || published != 0 || len(core.EnginePrivateStarts(target)) != 0 || core.EngineDatabaseAdmissionCount(target) != 0 || len(target.Storage.Relations) != 0 {
		t.Fatal("private initialization left late publication or resources")
	}
	if core.EngineRoleSessionCountLocked(e, admin.CountedAuthenticated) != 2 || len(e.Statistics.CaptureActivities()) != 2 {
		t.Fatal("private cleanup changed independent peer ownership/activity")
	}
}
