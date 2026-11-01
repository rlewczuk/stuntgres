package lock_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

type visibilityLockOwner struct{ids.TxOwnerMarker}
func TestPgvisibilityTupleLockFacts(t *testing.T) {
	ctx:=context.Background()
	domain:=new(ids.Domain)
	manager:=lock.New(domain)
	register:=func(id ids.SessionID)*lock.Ticket{
		t.Helper()
		ref:=domain.Tx(domain.Session(id),ids.TxID(id),1,&visibilityLockOwner{},uint32(id))
		ticket,err:=manager.Register(ref);if err!=nil{t.Fatal(err)}
		ticket.MirrorFullXID(uint64(100+id))
		t.Cleanup(func(){manager.End(ticket)})
		return ticket
	}
	observer,a,b:=register(1),register(2),register(3)
	tuple:=lock.MultiTuple{Resource:lock.Resource{ObjectRef:domain.Ref(1,ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:20000}),Row:1},Version:1}
	acquire:=func(ticket *lock.Ticket,mode lock.Mode,event lock.MultiMode){
		t.Helper()
		request,err:=manager.StartAcquire(ctx,ticket,tuple.Resource,mode,lock.Policy{})
		if err==nil&&request!=nil {err=request.Wait()};if err!=nil{t.Fatal(err)}
		if err:=manager.PhysicalEvents(ticket,[]lock.MultiEvent{{Tuple:tuple,Mode:event}});err!=nil{t.Fatal(err)}
	}
	capture:=func(want bool){
		t.Helper()
		before:=manager.Inspect(observer,a,b)
		assigned:=observer.AssignedFullXID()
		facts:=[]lock.TupleMaintenance{{Tuple:tuple},{Tuple:lock.MultiTuple{Resource:tuple.Resource,Version:tuple.Version+1}}}
		if err:=manager.CaptureTupleMaintenance(ctx,observer,facts);err!=nil{t.Fatal(err)}
		if facts[0].Present!=want||facts[1].Present {t.Fatal("maintenance ignored exact physical identity",facts,want)}
		after:=manager.Inspect(observer,a,b)
		if !reflect.DeepEqual(before,after)||observer.AssignedFullXID()!=assigned {t.Fatal("read capture mutated raw ownership, counters, queues or XID")}
	}
	capture(false)
	checkpoint:=manager.Checkpoint(a)
	acquire(a,lock.ForKeyShare,lock.MultiKeyShare);capture(true)
	acquire(b,lock.ForKeyShare,lock.MultiKeyShare);capture(true)
	if got:=manager.Inspect(a,b).MultiRows[tuple];len(got.Members)!=2||got.ID==0 {t.Fatal("fixture lacks actual multi-owner raw bag",got)}
	manager.End(b)
	if len(manager.Inspect(a).MultiRows[tuple].Members)<2 {t.Fatal("fixture lacks retained completed raw member")}
	capture(true)
	manager.Restore(a,checkpoint);capture(false)
	// Own updater/private-insertion rows can be absent from Pgrowlocks despite
	// real raw xmax. The maintenance API must not inherit that SQL filtering.
	acquire(a,lock.ForUpdate,lock.MultiUpdateAction)
	filtered,err:=manager.Pgrowlocks(ctx,a,[]lock.PgrowlocksTuple{{Tuple:tuple,PrivateInsertion:true}})
	if err!=nil||len(filtered)!=0 {t.Fatal("fixture did not reach own-updater filter",filtered,err)}
	capture(true)
	manager.DetachPrepared(a);capture(true)
	manager.End(a);capture(false)
	bad:=[]lock.TupleMaintenance{{Tuple:tuple}}
	bad[0].Tuple.Version=0
	if err:=manager.CaptureTupleMaintenance(ctx,observer,bad);!errors.Is(err,ids.ErrCatalogReplaced){t.Fatal("invalid tuple proof accepted",err)}
	canceled,cancel:=context.WithCancel(ctx);cancel()
	if err:=manager.CaptureTupleMaintenance(canceled,observer,[]lock.TupleMaintenance{{Tuple:tuple}});!errors.Is(err,context.Canceled){t.Fatal(err)}
	manager.End(observer)
	if err:=manager.CaptureTupleMaintenance(ctx,observer,[]lock.TupleMaintenance{{Tuple:tuple}});!errors.Is(err,ids.ErrCatalogReplaced){t.Fatal("ended capture authority accepted",err)}
}
