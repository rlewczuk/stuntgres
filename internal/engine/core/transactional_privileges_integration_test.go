package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestTransactionalPrivilegesAllocatorRefresh(t *testing.T) {
	f := newDefaultUnitFixture(t)
	ref, err := f.e.Begin(f.op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer f.e.Rollback(f.op, ref)
	f.grant(t, "tables", "select", nil)
	journal := f.op.Session.Transactions.Catalog
	peer, done := enginetest.RoleOperation(t, f.e, "postgres")
	defer done()
	if _, err := f.e.Execute(peer, enginetest.DefaultUnitStatement("sequences", "usage", []string{"default_owner"}, nil, []string{"default_reader"}), 0, nil); err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	before := f.e.RoleStore.NextRevision()
	err = core.EngineRefreshOperationRolesLocked(f.e, f.op)
	prepared, prepareErr := core.EnginePublicationRolesLocked(f.e, context.Background(), journal)
	unchanged := f.op.Session.Transactions.Catalog == journal && prepared == journal && f.e.RoleStore.NextRevision() == before
	f.e.Mu.Unlock()
	if err != nil || prepareErr != nil || !unchanged {
		t.Fatal("same-authority refresh/preparation changed logical delta or reserved a revision", err, prepareErr)
	}
	for _, frontier := range []uint64{f.e.RoleStore.Published.Revision + 10, ^uint64(0)} {
		f.e.Mu.Lock()
		f.e.RoleStore.InstallRevisionFrontierLocked(frontier)
		state, roles := f.e.Catalog.State, f.e.RoleStore.Published
		_, validationErr := core.EngineJournalRolePublicationLocked(f.e, journal, false)
		publication, materializeErr := core.EngineJournalRolePublicationLocked(f.e, journal, true)
		unpublished := f.e.Catalog.State == state && f.e.RoleStore.Published == roles && f.e.RoleStore.NextRevision() == frontier
		f.e.Mu.Unlock()
		if !unpublished {
			t.Fatal("validation/materialization installed roots or reserved a revision")
		}
		if frontier == ^uint64(0) {
			if validationErr == nil || materializeErr == nil {
				t.Fatal("exhausted canonical frontier accepted")
			}
		} else if validationErr != nil || materializeErr != nil || publication == nil || publication.Revision != frontier {
			t.Fatal("canonical frontier was ignored", validationErr, materializeErr)
		}
	}
}
