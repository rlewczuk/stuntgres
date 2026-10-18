package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/lock"
	"testing"
	"time"
)

func TestDeleteCoreMetadataQueuedComment(t *testing.T) {
	for _, tc := range []struct{ name, comment string }{
		{"table", "COMMENT ON TABLE t IS 'metadata only'"},
		{"column", "COMMENT ON COLUMN t.v IS 'metadata only'"},
	} {
		for _, commit := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/holder_rollback", true: "/holder_commit"}[commit], func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				c := sessionStart(t, e, nil)
				sessionSQL(t, c, "CREATE TABLE t(v INT)")
				sessionSQL(t, c, "INSERT INTO t VALUES(1)")
				ar := transactionBegin(t, a)
				deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
				br := transactionBegin(t, b)
				sessionSQL(t, b, "INSERT INTO t VALUES(7)")
				waiter := b.transactions.ticket
				target := catalogRef(t, e, e.CatalogRead(), "t")
				e.mu.Lock()
				row := e.storage[target.Address].versions[0].row
				e.mu.Unlock()
				resource := lock.Resource{ObjectRef: target, Row: row}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := deleteAsync(t, b, ctx, "DELETE FROM t WHERE v<=2")
				lockPending(t, e, 1)
				lockState := e.locks.Inspect()
				entry := lockState.Resources[resource]
				var request *lock.Waiter
				if entry != nil && len(entry.Pending) == 1 {
					request = &entry.Pending[0]
				}
				queued := request != nil && request.Ticket == waiter && request.Resource == resource && request.Mode == lock.ForUpdate && !request.Terminal && entry.Holders[a.transactions.ticket].Mode == lock.ForUpdate
				if !queued {
					t.Fatal("DELETE not actually queued on the original row resource")
				}
				// Finish compatible COMMENT before releasing the row holder.
				// The pending request, not a start channel, proves this interleaving.
				deleteAwait(t, deleteAsync(t, c, ctx, tc.comment), 0, nil)
				lockState = e.locks.Inspect()
				entry = lockState.Resources[resource]
				stillQueued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Request == request.Request && !entry.Pending[0].Terminal
				if !stillQueued {
					t.Fatal("COMMENT changed the queued row request")
				}
				// This matching insertion is after candidate capture and must
				// survive; the private INSERT also must survive the waiter commit.
				sessionSQL(t, c, "INSERT INTO t VALUES(2)")
				transactionEnd(t, a, ar, commit)
				want := int64(1)
				if commit {
					want = 0
				}
				deleteAwait(t, done, want, nil)
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 7)
				if commit {
					transactionRows(t, c, "SELECT v FROM t ORDER BY v", 2)
				} else {
					transactionRows(t, c, "SELECT v FROM t ORDER BY v", 1, 2)
				}
				if !transactionEnd(t, b, br, true).Committed {
					t.Fatal("waiter commit reported rollback")
				}
				transactionRows(t, c, "SELECT v FROM t ORDER BY v", 2, 7)
				lockEmpty(t, e)
			})
		}
	}
}

func TestDeleteCoreMetadataACLPublication(t *testing.T) {
	for _, column := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "column"}[column], func(t *testing.T) {
			e := New()
			op, cleanup := roleOperation(t, e, "postgres")
			roleExecute(t, e, op, "CREATE USER observer")
			owner := op.RoleState().CurrentUser
			observer, ok := e.Roles().Lookup("observer")
			if !ok {
				t.Fatal("missing observer role")
			}
			cleanup()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(1),(2),(2)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			ar := transactionBegin(t, a)
			sessionSQL(t, a, "INSERT INTO t VALUES(7)")
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			e.mu.Lock()
			beforeDescriptor := e.catalog.state.Get(ref.Address)
			beforeStorage := e.storage[ref.Address]
			beforeEpoch := e.catalog.state.Epoch()
			e.mu.Unlock()
			aclRef := ref
			if column {
				aclRef.Address.SubID = 1
			}
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: observer.Ref}, Privileges: authorization.SelectPrivilege}}}
			if err := e.SetObjectACL(context.Background(), aclRef, acl); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			metadataOnly := e.catalog.state.Get(ref.Address) != beforeDescriptor && e.storage[ref.Address] == beforeStorage && e.catalog.state.Epoch() == beforeEpoch
			e.mu.Unlock()
			if !metadataOnly {
				t.Fatal("fixture did not publish metadata-only ACL descriptor replacement")
			}
			transactionRows(t, a, "SELECT v FROM t ORDER BY v", 2, 2, 7)
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 2)
			if !transactionEnd(t, a, ar, true).Committed {
				t.Fatal("metadata-only ACL invalidated staged DELETE and prior INSERT")
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 2, 7)
			lockEmpty(t, e)
		})
	}
}

func TestDeleteCoreMetadataACLRevocationCommit(t *testing.T) {
	for _, column := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "column"}[column], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE USER deleter")
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(1),(2),(2)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			deleter, ok := e.Roles().Lookup("deleter")
			if !ok {
				t.Fatal("missing deleter role")
			}
			op := transactionOperation(t, b)
			owner := op.RoleState().CurrentUser
			op.Done()
			acl := func(privileges authorization.Privileges) authorization.ACLValue {
				return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: deleter.Ref}, Privileges: privileges}}}
			}
			mask := authorization.DeletePrivilege | authorization.SelectPrivilege
			if column {
				mask = authorization.DeletePrivilege
			}
			if err := e.SetObjectACL(context.Background(), ref, acl(mask)); err != nil {
				t.Fatal(err)
			}
			aclRef := ref
			if column {
				aclRef.Address.SubID = 1
				if err := e.SetObjectACL(context.Background(), aclRef, acl(authorization.SelectPrivilege)); err != nil {
					t.Fatal(err)
				}
			}
			sessionSQL(t, a, "SET SESSION AUTHORIZATION deleter")
			ar := transactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			if err := e.SetObjectACL(context.Background(), aclRef, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
			if !transactionEnd(t, a, ar, true).Committed {
				t.Fatal("ACL revocation retroactively rejected authorized staged DELETE")
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 2)
			lockEmpty(t, e)
		})
	}
}
