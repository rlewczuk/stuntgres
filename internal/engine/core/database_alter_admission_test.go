package core_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

type databaseAlterDefaults struct{ visit func() error }

func (d *databaseAlterDefaults) SessionDefault(_ context.Context, definition settings.Definition) (string, bool, error) {
	if definition.Name == "application_name" && d.visit != nil {
		visit := d.visit
		d.visit = nil
		return "", false, visit()
	}
	return "", false, nil
}

func databaseAlterEnd(t *testing.T, s *session.Session) {
	t.Helper()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseAlterAdmissionReservation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		label := "callback-failure"
		if canceled {
			label = "cancellation"
		}
		t.Run(label, func(t *testing.T) {
			e, principal := databaseAdmissionFixture()
			entered, release := make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			sentinel := errors.New("default provider rejected initialization")
			provider := &databaseAlterDefaults{visit: func() error {
				close(entered)
				select {
				case <-release:
					return sentinel
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			id := enginetest.NextSessionID()
			result := make(chan error, 1)
			joined := false
			go func() {
				s, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: id, Defaults: provider}, principal)
				if s != nil {
					s.RequestEnd(session.CleanClose)
					_ = s.WaitEnd(context.Background())
				}
				result <- err
			}()
			defer func() {
				cancel()
				if !joined {
					<-result
				}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if core.EngineDatabaseAdmissionCount(e) != 1 || len(core.EngineSessions(e)) != 0 || core.EngineRoleSessionCountLocked(e, principal) != 1 {
				t.Fatal("pending initializer was not reserved or was published early")
			}
			touched := false
			denied, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: enginetest.NextSessionID(), Defaults: &databaseAlterDefaults{visit: func() error { touched = true; return nil }}}, principal)
			if denied != nil || touched {
				t.Fatal("rejected admission reached defaults")
			}
			if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "53300" {
				t.Fatal("pending role count", err)
			}
			if canceled {
				cancel()
			} else {
				close(release)
			}
			err = <-result
			joined = true
			if canceled && !errors.Is(err, context.Canceled) || !canceled && !errors.Is(err, sentinel) {
				t.Fatal("initialization outcome", err)
			}
			if len(core.EngineSessions(e)) != 0 || core.EngineDatabaseAdmissionCount(e) != 0 {
				t.Fatal("initialization failure leaked physical reservation")
			}
			live, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: id}, principal)
			if err != nil {
				t.Fatal("reservation/ticket was not released", err)
			}
			databaseAlterEnd(t, live)
		})
	}
	e, principal := databaseAdmissionFixture()
	start := session.SessionStart{ID: enginetest.NextSessionID()}
	reservation, err := e.AdmitDatabase(context.Background(), principal, start.ID, core.DatabaseSelector{})
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	copied := *reservation
	if invalid, err := e.StartSession(context.Background(), nil, start); err == nil || invalid != nil {
		t.Fatal("nil reservation accepted")
	}
	empty := core.DatabaseAdmission{}
	if invalid, err := e.StartSession(context.Background(), &empty, start); err == nil || invalid != nil {
		t.Fatal("foreign empty reservation accepted")
	}
	if invalid, err := e.StartSession(context.Background(), &copied, start); err == nil || invalid != nil {
		t.Fatal("copied reservation accepted")
	}
	if core.EngineDatabaseAdmissionCount(e) != 1 {
		t.Fatal("invalid submission stole original reservation")
	}
	live, err := e.StartSession(context.Background(), reservation, start)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := e.StartSession(context.Background(), reservation, start); err == nil || duplicate != nil {
		t.Fatal("reservation consumed twice")
	}
	databaseAlterEnd(t, live)
	if stale, err := e.StartSession(context.Background(), reservation, start); err == nil || stale != nil {
		t.Fatal("retired reservation accepted")
	}
	foreign := engine.New()
	if a, err := e.AdmitDatabase(context.Background(), foreign.Roles().LookupRef("postgres"), enginetest.NextSessionID(), core.DatabaseSelector{}); err == nil || a != nil {
		t.Fatal("foreign principal admitted")
	}
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	database := *next.MustDatabase(e.Catalog.DatabaseAddress())
	database.AllowConnections = false
	database.ConnectionLimit = 0
	database.ObjectRevision++
	next.SetDescriptor(database.Address(), &database)
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	private, err := e.StartRestoreSession(context.Background(), principal)
	if err != nil {
		t.Fatal("maintenance admission", err)
	}
	core.EngineSessionMuRef(e).Lock()
	registered := len(core.EngineSessions(e)) == 1 && core.EngineSessions(e)[private.SessionToken] == private
	core.EngineSessionMuRef(e).Unlock()
	if !registered || private.SessionToken.ID() != 0 || private.SessionToken.Private() != private || core.EngineDatabaseAdmissionCount(e) != 0 {
		t.Fatal("private maintenance lost lifecycle ownership or counted publicly")
	}
	if core.EngineRoleSessionCountLocked(e, principal) != 0 || len(e.Statistics.CaptureActivities()) != 0 {
		t.Fatal("private maintenance acquired public role occupancy or activity")
	}
	databaseAlterEnd(t, private)
	core.EngineSessionMuRef(e).Lock()
	remaining := len(core.EngineSessions(e))
	core.EngineSessionMuRef(e).Unlock()
	if remaining != 0 || core.EngineDatabaseAdmissionCount(e) != 0 {
		t.Fatal("private maintenance leaked lifecycle ownership or admission")
	}
}

type databaseAlterLockOwner struct{ ids.TxOwnerMarker }

func TestDatabaseAlterRenameAdmissionRace(t *testing.T) {
	e := engine.NewWithSettings(nil, "race_source", false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	original := e.AttachedDatabaseName()
	token := e.Catalog.Domain.Session(enginetest.NextSessionID())
	tx := e.Catalog.Domain.Tx(token, 1, e.Catalog.State.Epoch(), &databaseAlterLockOwner{}, 1)
	ticket, err := e.Locks.Register(tx)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Locks.End(ticket)
	resource := e.DatabaseResource(e.Catalog.DatabaseAddress())
	if err := e.Locks.Acquire(ctx, ticket, resource, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	joined := false
	start := session.SessionStart{ID: enginetest.NextSessionID()}
	go func() {
		admission, err := e.AdmitDatabase(ctx, e.Roles().LookupRef("postgres"), start.ID, core.DatabaseSelector{Name: original, Explicit: true})
		if err == nil {
			defer admission.Release()
			s, startErr := admission.Target().StartSession(ctx, admission, start)
			err = startErr
			if s != nil {
				s.RequestEnd(session.CleanClose)
				_ = s.WaitEnd(context.Background())
			}
		}
		result <- err
	}()
	defer func() {
		cancel()
		e.Locks.End(ticket)
		if !joined {
			<-result
		}
	}()
	for {
		rows, err := e.Locks.PGLocks(e.Catalog.Domain, e.Catalog.State.Epoch())
		if err != nil {
			t.Fatal(err)
		}
		pending := false
		for _, row := range rows {
			if row.Waiting && row.Kind == lock.ObjectRecord && row.Class == uint32(ids.DatabaseClassID) {
				pending = true
			}
		}
		if pending {
			break
		}
		select {
		case err := <-result:
			joined = true
			t.Fatal("startup bypassed exclusion", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	old := next.MustDatabase(e.Catalog.DatabaseAddress())
	renamed := *old
	renamed.ObjectName = "race_renamed"
	renamed.ObjectRevision++
	next.UnbindName(catalog.DescriptorNameKey(old))
	next.SetDescriptor(renamed.Address(), &renamed)
	next.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
	publication, err := e.PrepareDatabasePublicationLocked(next)
	if err == nil {
		e.Catalog.PublishLocked(next)
		publication.InstallLocked(e)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.Locks.End(ticket)
	err = <-result
	joined = true
	if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "3D000" {
		t.Fatal("stale selector redirected", err)
	}
	if len(core.EngineSessions(e)) != 0 {
		t.Fatal("late old-name session registered")
	}
	start = session.SessionStart{ID: enginetest.NextSessionID()}
	reservation, err := e.AdmitDatabase(ctx, e.Roles().LookupRef("postgres"), start.ID, core.DatabaseSelector{})
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	e.Mu.Lock()
	rejected := catalog.CloneState(e.Catalog.State)
	current := rejected.MustDatabase(e.Catalog.DatabaseAddress())
	changed := *current
	changed.ObjectName = "must_not_publish"
	changed.ObjectRevision++
	rejected.UnbindName(catalog.DescriptorNameKey(current))
	rejected.SetDescriptor(changed.Address(), &changed)
	rejected.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	_, err = e.PrepareDatabasePublicationLocked(rejected)
	e.Mu.Unlock()
	if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "55006" {
		t.Fatal("reserved initializer did not block rename", err)
	}
	live, err := e.StartSession(ctx, reservation, start)
	if err != nil {
		t.Fatal(err)
	}
	if live.DatabaseName != "race_renamed" {
		t.Fatal("mutable runtime name raced physical capture")
	}
	databaseAlterEnd(t, live)
}
