package lock_test

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"testing"
	"time"
)

type detachLockOwner struct{ ids.TxOwnerMarker }

func TestPartitionDetachConcurrentCohort(t *testing.T) {
	for _, mode := range []lock.Mode{lock.AccessShare, lock.RowExclusive, lock.ShareUpdateExclusive, lock.AccessExclusive} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			domain := new(ids.Domain)
			m := lock.New(domain)
			ticket := func(id ids.TxID, session ids.SessionID) *lock.Ticket {
				token := domain.Session(session)
				owner, err := m.Register(domain.Tx(token, id, 1, &detachLockOwner{}, uint32(id)))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { m.End(owner) })
				return owner
			}
			self, original, sameSession := ticket(1, 1), ticket(2, 2), ticket(3, 1)
			resource := lock.Resource{ObjectRef: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17001})}
			grant := func(owner *lock.Ticket, resource lock.Resource, mode lock.Mode) {
				request, err := m.StartAcquire(context.Background(), owner, resource, mode, lock.Policy{NoWait: true})
				if err != nil || request != nil {
					t.Fatal("immediate grant", request, err)
				}
			}
			grant(original, resource, mode)
			cohort, err := m.RelationHolders(self, resource.ObjectRef, lock.AccessExclusive)
			if err != nil || len(cohort) != 1 || cohort[0] != original {
				t.Fatal("AX-conflict cohort", cohort, err)
			}
			m.End(original)
			grant(sameSession, resource, lock.AccessShare)
			cohort, err = m.RelationHolders(self, resource.ObjectRef, lock.AccessExclusive)
			if err != nil || len(cohort) != 0 {
				t.Fatal("self session entered cohort", cohort, err)
			}
			m.End(sameSession)
			// A separate phase example proves actual SUE release, later AS admission,
			// fixed cohort identity and virtual completion independent of relation holds.
			original = ticket(4, 4)
			child := lock.Resource{ObjectRef: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17002})}
			grant(self, resource, lock.ShareUpdateExclusive)
			grant(self, child, lock.ShareUpdateExclusive)
			grant(original, resource, lock.AccessShare)
			cohort, err = m.RetireDetachPhase(self, resource.ObjectRef, []ids.ObjectRef{child.ObjectRef})
			if err != nil || len(cohort) != 1 || cohort[0] != original {
				t.Fatal("phase cohort", cohort, err)
			}
			if m.HasRelationMode(self, resource, lock.ShareUpdateExclusive) || m.HasRelationMode(self, child, lock.ShareUpdateExclusive) {
				t.Fatal("first-phase SUE survived retirement")
			}
			later := ticket(5, 5)
			grant(later, resource, lock.AccessShare)
			if slices.Contains(cohort, later) {
				t.Fatal("later reader enlarged frozen cohort")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			wait, err := m.StartAcquire(ctx, self, lock.CompletionOf(original), lock.TransactionCompletion, lock.Policy{CompletionPurpose: lock.CompletionVirtual})
			if err != nil || wait == nil {
				t.Fatal("virtual completion admission", wait, err)
			}
			m.Restore(original, nil)
			if wait.Terminal() {
				t.Fatal("relation retirement impersonated transaction completion")
			}
			snapshot := m.Inspect()
			if parent := snapshot.Resources[resource]; parent != nil {
				if parent.Holders[self].Mode&lock.AccessExclusive != 0 {
					t.Fatal("granted parent AX")
				}
				for _, pending := range parent.Pending {
					if pending.Ticket == self {
						t.Fatal("queued parent acquisition", pending)
					}
				}
			}
			m.End(original)
			if err := wait.Wait(); err != nil {
				t.Fatal(err)
			}
			if !m.HasRelationMode(later, resource, lock.AccessShare) {
				t.Fatal("later reader ended to permit completion")
			}
		})
	}
}
