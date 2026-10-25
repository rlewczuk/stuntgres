package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestDeleteCoreMetadataQueuedComment(t *testing.T) {
	for _, tc := range []struct{ name, comment string }{
		{"table", "COMMENT ON TABLE t IS 'metadata only'"},
		{"column", "COMMENT ON COLUMN t.v IS 'metadata only'"},
	} {
		for _, commit := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/holder_rollback", true: "/holder_commit"}[commit], func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				c := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, c, "CREATE TABLE t(v INT)")
				enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1)")
				ar := enginetest.TransactionBegin(t, a)
				deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
				br := enginetest.TransactionBegin(t, b)
				enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
				waiter := b.Transactions.Ticket
				target := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				e.Mu.Lock()
				row := e.Storage.Relations[target.Address].Versions[0].Row
				e.Mu.Unlock()
				resource := lock.Resource{ObjectRef: target, Row: row}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := deleteAsync(t, b, ctx, "DELETE FROM t WHERE v<=2")
				enginetest.LockPending(t, e, 1)
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[resource]
				var request *lock.Waiter
				if entry != nil && len(entry.Pending) == 1 {
					request = &entry.Pending[0]
				}
				queued := request != nil && request.Ticket == waiter && request.Resource == resource && request.Mode == lock.ForUpdate && !request.Terminal && entry.Holders[a.Transactions.Ticket].Mode == lock.ForUpdate
				if !queued {
					t.Fatal("DELETE not actually queued on the original row resource")
				}
				// Finish compatible COMMENT before releasing the row holder.
				// The pending request, not a start channel, proves this interleaving.
				deleteAwait(t, deleteAsync(t, c, ctx, tc.comment), 0, nil)
				lockState = e.Locks.Inspect()
				entry = lockState.Resources[resource]
				stillQueued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Request == request.Request && !entry.Pending[0].Terminal
				if !stillQueued {
					t.Fatal("COMMENT changed the queued row request")
				}
				// This matching insertion is after candidate capture and must
				// survive; the private INSERT also must survive the waiter commit.
				enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(2)")
				enginetest.TransactionEnd(t, a, ar, commit)
				want := int64(1)
				if commit {
					want = 0
				}
				deleteAwait(t, done, want, nil)
				enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 7)
				if commit {
					enginetest.TransactionRows(t, c, "SELECT v FROM t ORDER BY v", 2)
				} else {
					enginetest.TransactionRows(t, c, "SELECT v FROM t ORDER BY v", 1, 2)
				}
				if !enginetest.TransactionEnd(t, b, br, true).Committed {
					t.Fatal("waiter commit reported rollback")
				}
				enginetest.TransactionRows(t, c, "SELECT v FROM t ORDER BY v", 2, 7)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestDeleteCoreMetadataACLPublication(t *testing.T) {
	for _, column := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "column"}[column], func(t *testing.T) {
			e := engine.New()
			op, cleanup := enginetest.RoleOperation(t, e, "postgres")
			enginetest.RoleExecute(t, e, op, "CREATE USER observer")
			owner := op.RoleState().CurrentUser
			observer, ok := e.Roles().Lookup("observer")
			if !ok {
				t.Fatal("missing observer role")
			}
			cleanup()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1),(2),(2)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			ar := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			e.Mu.Lock()
			beforeDescriptor := e.Catalog.State.Get(ref.Address)
			beforeStorage := e.Storage.Relations[ref.Address]
			beforeEpoch := e.Catalog.State.Epoch()
			e.Mu.Unlock()
			aclRef := ref
			if column {
				aclRef.Address.SubID = 1
			}
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: observer.Ref}, Privileges: authorization.SelectPrivilege}}}
			if err := enginetest.SetObjectACL(e, context.Background(), aclRef, acl); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			metadataOnly := e.Catalog.State.Get(ref.Address) != beforeDescriptor && e.Storage.Relations[ref.Address] == beforeStorage && e.Catalog.State.Epoch() == beforeEpoch
			e.Mu.Unlock()
			if !metadataOnly {
				t.Fatal("fixture did not publish metadata-only ACL descriptor replacement")
			}
			enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 2, 2, 7)
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 2)
			if !enginetest.TransactionEnd(t, a, ar, true).Committed {
				t.Fatal("metadata-only ACL invalidated staged DELETE and prior INSERT")
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 2, 7)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestDeleteCoreMetadataACLRevocationCommit(t *testing.T) {
	for _, column := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "column"}[column], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE USER deleter")
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1),(2),(2)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			deleter, ok := e.Roles().Lookup("deleter")
			if !ok {
				t.Fatal("missing deleter role")
			}
			op := enginetest.TransactionOperation(t, b)
			owner := op.RoleState().CurrentUser
			op.Done()
			acl := func(privileges authorization.Privileges) authorization.ACLValue {
				return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: deleter.Ref}, Privileges: privileges}}}
			}
			mask := authorization.DeletePrivilege | authorization.SelectPrivilege
			if column {
				mask = authorization.DeletePrivilege
			}
			if err := enginetest.SetObjectACL(e, context.Background(), ref, acl(mask)); err != nil {
				t.Fatal(err)
			}
			aclRef := ref
			if column {
				aclRef.Address.SubID = 1
				if err := enginetest.SetObjectACL(e, context.Background(), aclRef, acl(authorization.SelectPrivilege)); err != nil {
					t.Fatal(err)
				}
			}
			enginetest.SessionSQL(t, a, "SET SESSION AUTHORIZATION deleter")
			ar := enginetest.TransactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			if err := enginetest.SetObjectACL(e, context.Background(), aclRef, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
			if !enginetest.TransactionEnd(t, a, ar, true).Committed {
				t.Fatal("ACL revocation retroactively rejected authorized staged DELETE")
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 2)
			enginetest.LockEmpty(t, e)
		})
	}
}
