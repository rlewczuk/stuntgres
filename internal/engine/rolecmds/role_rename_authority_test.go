package rolecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// A private CREATE holds the destination name but is invisible to the rename's
// first decision. Revoking the actor while it waits distinguishes readmission
// from a cached capability; the source tuple remains entirely unchanged.
func TestRoleRenameWaitAuthority(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	d := roleAttributeCreate(t, e, admin, "rename_fenced", false, parser.RoleOptions{})
	actor := roleAttributeCreate(t, e, admin, "rename_actor", true, parser.RoleOptions{Present: parser.RoleCreateRole, CreateRole: true})
	if _, err := e.Execute(admin, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: d.Name}}, Members: []parser.RoleSpec{{Name: actor.Name}}, Options: []parser.MembershipOption{{Name: "admin", Value: true}}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	holder := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, holder)
	hold := enginetest.TransactionOperation(t, holder)
	if _, err := e.Execute(hold, &parser.CreateRole{Name: parser.Identifier{Text: "rename_destination"}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	hold.Done()
	waiter, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}, actor.Ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		waiter.RequestEnd(session.CleanClose)
		if err := waiter.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		op, err := waiter.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, renameStatement(d.Name, "rename_destination"), 0, nil)
			op.Done()
		}
		result <- err
	}()
	enginetest.LockPending(t, e, 1)
	found := false
	for resource, state := range e.Locks.Inspect().Resources {
		if len(state.Pending) == 0 {
			continue
		}
		if resource.Domain() != &e.Cluster.RoleDomain || resource.Name.Name != "rename_destination" || state.Pending[0].Mode != lock.AccessExclusive {
			t.Fatal("rename waited on wrong resource", resource, state.Pending)
		}
		found = true
	}
	if !found {
		t.Fatal("destination writer not admitted")
	}
	roleAttributeApply(t, e, admin, roleAttributeAlter(actor.Name, parser.RoleOptions{Present: parser.RoleCreateRole}))
	before := e.RoleStore.Published
	if enginetest.TransactionEnd(t, holder, ref, false).Committed {
		t.Fatal("destination rollback committed")
	}
	select {
	case err := <-result:
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied to rename role" {
			t.Fatal("wait retained revoked authority", err)
		}
	case <-contextDone(t):
		t.Fatal("rename did not finish after destination release")
	}
	if e.RoleStore.Published != before {
		t.Fatal("rejected rename replaced publication")
	}
	if got, exists := e.Roles().Find(d.Ref); !exists || got != d || e.Roles().LookupRef("rename_destination") != (authorization.PrincipalRef{}) {
		t.Fatal("rejected rename changed source identity", got)
	}
	enginetest.LockEmpty(t, e)
}

func TestRoleRenameWaitTupleVersion(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	d := roleAttributeCreate(t, e, admin, "rename_version", false, parser.RoleOptions{})
	done()
	holder, waiter := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, holder)
	hold := enginetest.TransactionOperation(t, holder)
	if _, err := e.Execute(hold, roleAttributeAlter(d.Name, parser.RoleOptions{Present: parser.RoleLogin, Login: true}), 0, nil); err != nil {
		t.Fatal(err)
	}
	hold.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		op, err := waiter.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, renameStatement(d.Name, "rename_version_next"), 0, nil)
			op.Done()
		}
		result <- err
	}()
	enginetest.LockPending(t, e, 1)
	for resource, state := range e.Locks.Inspect().Resources {
		if len(state.Pending) == 0 {
			continue
		}
		if resource.Domain() != &e.Cluster.RoleDomain || !resource.RoleTuple || resource.Address.ObjectID != ids.OID(d.Ref.ID()) || state.Pending[0].Mode != lock.AccessExclusive {
			t.Fatal("wrong source tuple writer", resource, state.Pending)
		}
	}
	if !enginetest.TransactionEnd(t, holder, ref, true).Committed {
		t.Fatal("holder failed to commit")
	}
	select {
	case err := <-result:
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "tuple concurrently updated" {
			t.Fatal("rename adopted changed source tuple", err)
		}
	case <-contextDone(t):
		t.Fatal("rename did not finish after source release")
	}
	got, exists := e.Roles().Find(d.Ref)
	if !exists || got.Name != d.Name || !got.Login || got.AuthIDVersion == d.AuthIDVersion || e.Roles().LookupRef("rename_version_next") != (authorization.PrincipalRef{}) {
		t.Fatal("rename replaced committed predecessor", got)
	}
	enginetest.LockEmpty(t, e)
}
