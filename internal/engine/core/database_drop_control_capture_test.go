package core_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func databaseDropControlFixture(t *testing.T, simulation bool) (*core.Engine, *core.Engine, *session.Session) {
	t.Helper()
	if !simulation {
		return databaseDropOwnerFixture(t)
	}
	// Install a genuine engine policy before creating any target or physical
	// subject. CREATE inherits that exact policy; no fake authority is minted.
	e := sepgsqlPhaseEngine(t)
	admin := enginetest.StartSession(t, e, nil)
	if _, err := databaseDropOwnerExecute(t.Context(), e, admin, &parser.CreateDatabaseStmt{Name: "drop_owner_target"}); err != nil {
		t.Fatal(err)
	}
	target, err := e.SelectDatabase(t.Context(), "drop_owner_target")
	if err != nil {
		t.Fatal(err)
	}
	if target.Sepgsql == nil || target.Sepgsql != e.Sepgsql {
		t.Fatal("target did not inherit active engine simulation")
	}
	return e, target, admin
}

func databaseDropDetachedAuthority(t *testing.T, target *core.Engine) *core.ControlSepgsqlAuthority {
	t.Helper()
	physical := enginetest.StartSession(t, target, nil)
	authority, err := target.CaptureControlSepgsql(t.Context(), physical, nil)
	if err != nil || (target.Sepgsql != nil && authority == nil) {
		t.Fatal("genuine control admission failed", err)
	}
	// Changing the borrower's subject after capture must not recapture or lend
	// authority to a later control. Its physical lifetime ends before host work.
	if target.Sepgsql != nil {
		enginetest.SessionSQL(t, physical, "SELECT sepgsql_setcon('R')")
	}
	physical.RequestEnd(session.CleanClose)
	if err := physical.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
	return authority
}

// Err is an existing unlocked host callback at both boundaries under test:
// Reset checks it once before its destination capture, then at replacement
// admission after building the genuine fresh source. Inspect checks it before
// capture and after projecting actual immutable index flags. The second call is
// therefore a deterministic capture-to-final barrier, not a production hook.
type databaseDropControlContext struct {
	context.Context
	calls            atomic.Uint32
	entered, release chan struct{}
}

func (c *databaseDropControlContext) Err() error {
	if c.calls.Add(1) == 2 {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

func TestDatabaseDropControlCapture(t *testing.T) {
	for _, mode := range []struct {
		name       string
		simulation bool
	}{{"no_simulation", false}, {"active_simulation", true}} {
		t.Run(mode.name, func(t *testing.T) {
			for _, operation := range []string{"reset", "inspect"} {
				t.Run(operation, func(t *testing.T) {
					e, target, admin := databaseDropControlFixture(t, mode.simulation)
					enginetest.Run(t, target, "CREATE TABLE drop_capture_backing(v integer)")
					enginetest.Run(t, target, "INSERT INTO drop_capture_backing VALUES(11)")
					enginetest.Run(t, target, "CREATE INDEX drop_capture_index ON drop_capture_backing(v)")
					enginetest.Run(t, e, "CREATE TABLE drop_capture_peer(v integer)")
					enginetest.Run(t, e, "INSERT INTO drop_capture_peer VALUES(22)")
					prior := target.CatalogRead()
					oldAddress, oldDomain := target.Catalog.DatabaseAddress(), target.Catalog.Domain
					ref := enginetest.RoleTableRef(t, target, "drop_capture_backing")
					stored := target.Storage.Relations[ref.Address]
					peerRef := enginetest.RoleTableRef(t, e, "drop_capture_peer")
					peerStored := e.Storage.Relations[peerRef.Address]
					authority := databaseDropDetachedAuthority(t, target)
					if mode.simulation {
						if err := e.ValidateControlSepgsql(authority); err == nil {
							t.Fatal("foreign target adopted control owner/policy")
						}
						if err := engine.ResetWithAuthority(t.Context(), target, nil); err == nil {
							t.Fatal("active simulation accepted missing physical authority")
						}
					}
					ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
					defer cancel()
					barrier := &databaseDropControlContext{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
					var once sync.Once
					unblock := func() { once.Do(func() { close(barrier.release) }) }
					done, dropDone := make(chan error, 1), make(chan error, 1)
					joined, dropStarted, dropJoined := false, false, false
					defer func() {
						unblock()
						cancel()
						if !joined {
							<-done
						}
						if dropStarted && !dropJoined {
							<-dropDone
						}
					}()
					go func() {
						if operation == "reset" {
							done <- engine.ResetWithAuthority(barrier, target, authority)
							return
						}
						ready, valid, live, older, writers, err := target.InspectIndex(barrier, "public", "drop_capture_index")
						if ready || valid || live || older || writers {
							done <- core.SQLError("XX000", "stale inspection published old flags")
							return
						}
						done <- err
					}()
					select {
					case <-barrier.entered:
					case err := <-done:
						joined = true
						t.Fatal("control did not reach capture-to-final boundary", err)
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					// A control regression that holds a lock across its callback must
					// not strand the test in synchronous DROP. Failure cleanup releases
					// the capture barrier before joining either real operation.
					dropStarted = true
					go func() {
						e.Mu.Lock()
						unchanged := target.Catalog.Current().State() == prior.State() && target.Storage.Relations[ref.Address] == stored
						e.Mu.Unlock()
						if !unchanged {
							dropDone <- fmt.Errorf("capture changed target backing before final publication")
							return
						}
						_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(false))
						dropDone <- err
					}()
					dropErr := databaseDropReceive(t, dropDone)
					dropJoined = true
					if dropErr != nil {
						t.Fatal("captured control retained a public owner or lock", dropErr)
					}
					if _, err := databaseDropOwnerExecute(ctx, e, admin, &parser.CreateDatabaseStmt{Name: target.DatabaseName}); err != nil {
						t.Fatal(err)
					}
					fresh, err := e.SelectDatabase(ctx, target.DatabaseName)
					if err != nil {
						t.Fatal(err)
					}
					enginetest.Run(t, fresh, "CREATE TABLE drop_capture_backing(v integer)")
					enginetest.Run(t, fresh, "INSERT INTO drop_capture_backing VALUES(33)")
					enginetest.Run(t, fresh, "CREATE INDEX drop_capture_index ON drop_capture_backing(v)")
					freshRef := enginetest.RoleTableRef(t, fresh, "drop_capture_backing")
					freshStored := fresh.Storage.Relations[freshRef.Address]
					freshRoot, peerRoot := fresh.CatalogRead().State(), e.CatalogRead().State()
					if fresh == target || fresh.Catalog.DatabaseAddress() == oldAddress || fresh.Catalog.Domain == oldDomain || freshStored == stored || freshStored == nil {
						t.Fatal("recreated name adopted old runtime/identity/backing")
					}
					unblock()
					controlErr := databaseDropReceive(t, done)
					joined = true
					databaseDropOrdinaryRetired(t, controlErr)
					if fresh.CatalogRead().State() != freshRoot || e.CatalogRead().State() != peerRoot || fresh.Storage.Relations[freshRef.Address] != freshStored || e.Storage.Relations[peerRef.Address] != peerStored {
						t.Fatal("stale control changed replacement/peer roots or exact backing")
					}
					if len(target.Storage.Relations) != 0 || prior.Get(ref.Address) == nil || len(stored.Versions) != 1 || len(freshStored.Versions) != 1 || len(peerStored.Versions) != 1 {
						t.Fatal("stale control resurrected or mutated detached backing")
					}
					ready, valid, live, older, writers, err := fresh.InspectIndex(ctx, "public", "drop_capture_index")
					if err != nil || !ready || !valid || !live || older || writers {
						t.Fatal("fresh exact incarnation inspection failed", ready, valid, live, older, writers, err)
					}
				})
			}
		})
	}
}
