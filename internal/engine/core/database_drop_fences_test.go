package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func databaseDropReservedPreflight(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		e, target, admin := databaseDropOwnerFixture(t)
		enginetest.SessionSQL(t, admin, "CREATE ROLE drop_reserved_owner LOGIN")
		enginetest.SessionSQL(t, admin, "CREATE ROLE drop_reserved_other LOGIN")
		enginetest.SessionSQL(t, admin, "ALTER DATABASE drop_owner_target OWNER TO drop_reserved_owner")
		owner, other := e.Roles().LookupRef("drop_reserved_owner"), e.Roles().LookupRef("drop_reserved_other")
		issuer, err := enginetest.OpenSession(t.Context(), e, session.SessionStart{ID: enginetest.NextSessionID()}, owner)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			issuer.RequestEnd(session.CleanClose)
			if err := issuer.WaitEnd(context.Background()); err != nil {
				t.Error(err)
			}
		})
		principals := []bool{false, true}
		if reverse {
			principals = []bool{true, false}
		}
		var reservations []*core.DatabaseAdmission
		for _, ineligible := range principals {
			principal := owner
			if ineligible {
				principal = other
			}
			a, err := e.AdmitDatabase(t.Context(), principal, enginetest.NextSessionID(), core.DatabaseSelector{Name: target.DatabaseName, Explicit: true})
			if err != nil {
				t.Fatal(err)
			}
			reservations = append(reservations, a)
			t.Cleanup(a.Release)
		}
		_, err = databaseDropOwnerExecute(t.Context(), e, issuer, databaseDropOwnerStatement(true))
		enginetest.State(t, err, "42501")
		if core.EngineDatabaseAdmissionCount(target) != 2 {
			t.Fatal("permission preflight revoked an earlier reservation")
		}
		for _, a := range reservations {
			s, err := target.StartSession(t.Context(), a, session.SessionStart{ID: core.DatabaseAdmissionSessionID(a)})
			if err != nil {
				t.Fatal("preflight changed reservation authority", err)
			}
			s.RequestEnd(session.CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if core.EngineDatabaseAdmissionCount(target) != 0 {
			t.Fatal("reservation teardown leaked")
		}
	}
}

func databaseDropSettingsFence(t *testing.T) {
	for _, qualified := range []bool{true, false} {
		e, target, admin := databaseDropOwnerFixture(t)
		enginetest.SessionSQL(t, admin, "CREATE ROLE drop_pending_defaults")
		writer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, writer, "BEGIN")
		if qualified {
			enginetest.SessionSQL(t, writer, "ALTER ROLE drop_pending_defaults IN DATABASE drop_owner_target SET application_name='stale'")
		} else {
			enginetest.SessionSQL(t, writer, "ALTER ROLE drop_pending_defaults SET application_name='survives'")
		}
		oldAddress := target.Catalog.DatabaseAddress()
		if _, err := databaseDropOwnerExecute(t.Context(), e, admin, databaseDropOwnerStatement(false)); err != nil {
			t.Fatal(err)
		}
		_, err := databaseDropOwnerExecute(t.Context(), e, writer, enginetest.Statement(t, "COMMIT"))
		if qualified && err == nil {
			t.Fatal("retained database-qualified role journal committed after retirement")
		}
		if !qualified && err != nil {
			t.Fatal("disjoint global settings work did not survive retirement", err)
		}
		for key, row := range e.RoleStore.Published.Settings.Rows() {
			if key.Database == oldAddress {
				t.Fatal("stale journal resurrected retired scope")
			}
			if !qualified && key.Role == e.Roles().LookupRef("drop_pending_defaults") && (len(row) != 1 || row[0].Value != "survives") {
				t.Fatal("global default row changed")
			}
		}
	}
}

func databaseDropRevisionFailure(t *testing.T) {
	e, target, admin := databaseDropOwnerFixture(t)
	victim := enginetest.StartSession(t, target, nil)
	targetRoot, peerRoot := target.CatalogRead().State(), e.CatalogRead().State()
	e.Mu.Lock()
	record := e.RoleStore.Published.Advance()
	record.Revision = ^uint64(0)
	e.RoleStore.PublishLocked(record)
	e.Mu.Unlock()
	_, err := databaseDropOwnerExecute(t.Context(), e, admin, databaseDropOwnerStatement(true))
	if err == nil || err.Error() != "stuntgres: authorization revision exhausted" {
		t.Fatal("revision exhaustion did not refuse before signaling", err)
	}
	if target.CatalogRead().State() != targetRoot || e.CatalogRead().State() != peerRoot || core.EngineDatabaseAdmissionCount(target) != 1 {
		t.Fatal("failed retirement changed catalogs or owners")
	}
	select {
	case <-victim.EndRequested():
		t.Fatal("revision failure signaled victim")
	default:
	}
}
