package lock

import (
	"context"
	"math"
	"runtime"
	"strconv"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

// Advisory resources project their unsigned key halves to classid/objid and
// their key space to objsubid; relation and type resources project their OID.
func TestPGLocksResourceRecords(t *testing.T) {
	owner := RecordOwner{Session: 7, Local: 9}
	for _, kind := range []ResourceKind{AdvisoryBigintResource, AdvisoryPairResource} {
		r, err := pgResourceRecord(Resource{Kind: kind, High: math.MaxUint32, Low: 1 << 31}, owner, AdvisoryShare|AdvisoryExclusive)
		want := Record{Kind: AdvisoryRecord, Owner: owner, Mode: AdvisoryShare | AdvisoryExclusive, Class: math.MaxUint32, Object: 1 << 31, Subobject: int16(kind)}
		if err != nil || r != want {
			t.Fatal("advisory key space", r, err)
		}
	}
	f := newLockFixture(t)
	relation, typ := f.relation(), f.typeResource()
	if r, err := pgResourceRecord(relation, owner, AccessShare); err != nil || r != (Record{Kind: RelationRecord, Owner: owner, Mode: AccessShare, Object: uint32(relation.Address.ObjectID)}) {
		t.Fatal("relation record", r, err)
	}
	if r, err := pgResourceRecord(typ, owner, TypeExclusive); err != nil || r != (Record{Kind: ObjectRecord, Owner: owner, Mode: TypeExclusive, Object: uint32(typ.Address.ObjectID), Class: uint32(ids.TypeClassID)}) {
		t.Fatal("type record", r, err)
	}
}

// The manager mutex does not exclude canonical XID assignment under Engine.mu.
// Queue genuine key dependencies before racing that first assignment with a
// capture. Extra genuine grants widen the existing capture interval without
// introducing a production callback, observer hook, timer or fake XID allocator.
// Scheduling cannot force the precise atomic-load interleaving (especially with
// one execution thread); every captured outcome must nevertheless be coherent.
func TestPGLocksConcurrentAssignmentCapture(t *testing.T) {
	for trial := range 16 {
		t.Run(strconv.Itoa(trial), func(t *testing.T) {
			f := newLockFixture(t)
			holder := f.tx()
			table := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999}
			resource := Resource{ObjectRef: f.domain.Ref(f.epoch, table)}
			resource.key = &UniqueKey{bucket: uniqueLockBucket{ObjectRef: resource.ObjectRef, hash: 17}}
			if err := f.m.Acquire(context.Background(), holder, resource, UniqueClaim); err != nil {
				t.Fatal(err)
			}
			for index := range 1024 {
				granted := Resource{ObjectRef: f.domain.Ref(f.epoch, table)}
				granted.Address.ObjectID += ids.OID(index + 1)
				if err := f.m.Acquire(context.Background(), holder, granted, AccessShare); err != nil {
					t.Fatal(err)
				}
			}
			const waiterCount = 8
			waiters := make(map[ids.SessionID]ids.TxID, waiterCount)
			for range waiterCount {
				waiter := f.tx()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(cancel)
				request, err := f.m.StartAcquire(ctx, waiter, resource, UniqueClaim, Policy{CompletionPurpose: CompletionTransaction})
				if err != nil {
					t.Fatal(err)
				}
				if request == nil {
					t.Fatal("logical dependency did not enqueue")
				}
				waiters[waiter.Ref().Session().ID()] = waiter.Ref().ID()
			}
			if holder.AssignedPublicXID() != 0 {
				t.Fatal("key grant assigned an XID before the genuine assignment")
			}
			type outcome struct {
				records []Record
				err     error
			}
			started := make(chan struct{})
			done := make(chan outcome, 1)
			joined := false
			t.Cleanup(func() {
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("identity capture did not join")
					}
				}
			})
			f.m.mu.Lock()
			go func() {
				close(started)
				records, err := f.m.PGLocks(f.domain, holder.Ref().Epoch())
				done <- outcome{records, err}
			}()
			<-started
			f.m.mu.Unlock()
			var captured outcome
			// Admit assignment while capture actually holds manager metadata,
			// rather than relying on goroutine launch or a sleep.
			deadline := time.NewTimer(4 * time.Second)
			defer deadline.Stop()
			for !joined {
				select {
				case captured = <-done:
					joined = true
				case <-deadline.C:
					t.Fatal("capture admission deadline")
				default:
				}
				if joined {
					break
				}
				if !f.m.mu.TryLock() {
					break
				}
				f.m.mu.Unlock()
				runtime.Gosched()
			}
			// The canonical assignment publishes through the ticket's atomic
			// mirror, which is all the manager's capture observes.
			const xid = 3
			holder.MirrorFullXID(xid)
			if !joined {
				select {
				case captured = <-done:
					joined = true
				case <-deadline.C:
					t.Fatal("identity capture completion deadline")
				}
			}
			if captured.err != nil {
				t.Fatal(captured.err)
			}
			var identity Record
			seen := make(map[ids.SessionID]bool, waiterCount)
			for _, record := range captured.records {
				local, wanted := waiters[record.Owner.Session]
				if !record.Waiting || !wanted {
					continue
				}
				if record.Owner.Local != local || seen[record.Owner.Session] {
					t.Fatal("queued owner identity/multiplicity changed", record)
				}
				if len(seen) == 0 {
					identity = record
				} else if record.Kind != identity.Kind || record.XID != identity.XID || record.Target != identity.Target {
					t.Fatal("one holder has inconsistent captured dependency identities", identity, record)
				}
				if record.Target.Session != holder.Ref().Session().ID() || record.Target.Local != holder.Ref().ID() {
					t.Fatal("logical dependency changed its actual holder", record)
				}
				seen[record.Owner.Session] = true
			}
			if len(seen) != waiterCount {
				t.Fatal("capture omitted queued logical dependencies", len(seen), waiterCount)
			}
			owners := 0
			for _, record := range captured.records {
				if record.Waiting || record.Owner.Session != holder.Ref().Session().ID() || record.Owner.Local != holder.Ref().ID() || record.Kind != identity.Kind {
					continue
				}
				if identity.Kind == VirtualRecord && record.Target == identity.Target || identity.Kind == TransactionRecord && record.XID == identity.XID {
					owners++
				}
			}
			if owners != 1 || identity.Kind == TransactionRecord && identity.XID != xid {
				t.Fatal("queued dependency lacks its corresponding captured exclusive owner", identity, "matching owners", owners, "assigned XID", xid)
			}
		})
	}
}
