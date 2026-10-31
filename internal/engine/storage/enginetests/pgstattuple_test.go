package enginetests

import (
	"context"
	"errors"
	"math"
	"testing"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestPgstattupleHeapClassification(t *testing.T) {
	domain := new(ids.Domain)
	own := domain.Tx(domain.Session(1), 1, 1, nil, 0)
	peer := domain.Tx(domain.Session(2), 2, 1, nil, 0)
	for _, tc := range []struct {
		name string
		born ids.CommitSeq
		inserter, ender ids.TxRef
		end ids.CommitSeq
		exact, approximate bool
	}{
		{"committed live", 1, ids.TxRef{}, ids.TxRef{}, 0, true, true},
		{"own insertion", 0, own, ids.TxRef{}, 0, true, false},
		{"peer insertion", 0, peer, ids.TxRef{}, 0, true, false},
		{"own insertion and real end", 0, own, own, 0, false, true},
		{"peer insertion and real end", 0, peer, peer, 0, true, false},
		{"own committed origin end", 1, ids.TxRef{}, own, 0, false, true},
		{"peer committed origin end", 1, ids.TxRef{}, peer, 0, true, true},
		{"committed ended", 1, ids.TxRef{}, ids.TxRef{}, 2, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fact := storage.HeapTupleFact{Version:storage.RowVersion{Born:tc.born}, Inserter:tc.inserter, Ender:tc.ender, End:tc.end}
			if got:=storage.HeapTupleLive(fact,own,false); got!=tc.exact { t.Fatalf("Dirty=%v want %v",got,tc.exact) }
			if got:=storage.HeapTupleLive(fact,own,true); got!=tc.approximate { t.Fatalf("Vacuum=%v want %v",got,tc.approximate) }
		})
	}
}

func TestPgstattupleHeapLayout(t *testing.T) {
	ctx := context.Background()
	for _, tc:=range []struct{ live,dead,liveBytes,deadBytes,pages,free int64 }{
		{0,0,0,0,0,0},
		{1,0,40,0,1,8124},
		{1,2,40,80,1,8036},
		{1,0,8168,0,2,8164},
	} {
		for _,approx:=range []bool{false,true} {
			s,err:=storage.HeapStatisticsLayout(ctx,tc.live,tc.dead,tc.liveBytes,tc.deadBytes,approx)
			if err!=nil || s.TableLen!=tc.pages*8192 || s.FreeSpace!=tc.free || s.TupleCount!=tc.live || s.DeadCount!=tc.dead { t.Fatalf("%+v -> %+v %v",tc,s,err) }
			if s.TableLen==0 {
				if s.TuplePercent!=0 || s.DeadPercent!=0 || s.FreePercent!=0 || s.ScannedPercent!=0 { t.Fatal(s) }
			} else {
				if s.ScannedPercent!=100 || tc.liveBytes+tc.deadBytes+s.FreeSpace>=s.TableLen { t.Fatal(s) }
				want:=100*float64(tc.liveBytes)/float64(s.TableLen)
				if !approx { want=math.Round(want*100)/100 }
				if s.TuplePercent!=want || math.IsNaN(s.FreePercent) || math.IsInf(s.FreePercent,0) { t.Fatal(s,want) }
			}
		}
	}
	for _, tc:=range [][4]int64{{-1,0,0,0},{0,0,-1,0},{math.MaxInt64,1,0,0},{math.MaxInt64/4+1,0,0,0},{1,0,math.MaxInt64,0}} {
		_,err:=storage.HeapStatisticsLayout(ctx,tc[0],tc[1],tc[2],tc[3],false)
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || diagnostic.Code!="22003" { t.Fatal(tc,err) }
	}
	ctx,cancel:=context.WithCancel(ctx)
	cancel()
	if _,err:=storage.HeapStatisticsLayout(ctx,1,0,40,0,false); !errors.Is(err,context.Canceled) { t.Fatal(err) }
	if _,err:=storage.CaptureHeapStatistics(ctx,nil,ids.TxRef{},false); !errors.Is(err,context.Canceled) { t.Fatal(err) }
}

func TestPgstattupleHeapRetention(t *testing.T) {
	tail:=&storage.HeapPredecessor{Version:storage.RowVersion{Row:1,Version:1,Born:1},End:2}
	middle:=&storage.HeapPredecessor{Version:storage.RowVersion{Row:1,Version:2,Born:2},End:3,Next:tail}
	root:=&storage.HeapPredecessor{Version:storage.RowVersion{Row:1,Version:3,Born:3},End:4,Next:middle}
	same,err:=storage.MapHeapPredecessors(root,func(row storage.RowVersion)(storage.RowVersion,bool,error){return row,false,nil})
	if err!=nil || same!=root {t.Fatal("no-op type transport replaced immutable root",err)}
	mapped,err:=storage.MapHeapPredecessors(root,func(row storage.RowVersion)(storage.RowVersion,bool,error){
		if row.Version!=2{return row,false,nil}
		row.Frozen=true
		return row,true,nil
	})
	if err!=nil || mapped==root || mapped.Next==middle || mapped.Next.Next!=tail || !mapped.Next.Version.Frozen || middle.Version.Frozen || mapped.End!=root.End || mapped.Next.End!=middle.End {t.Fatal("header transport failed COW prefix/unchanged suffix ownership",err)}
	ctx,cancel:=context.WithCancel(context.Background())
	cancel()
	_,err=storage.MapHeapPredecessors(root,func(row storage.RowVersion)(storage.RowVersion,bool,error){return row,false,ctx.Err()})
	if !errors.Is(err,context.Canceled) || root.Next!=middle || middle.Next!=tail || middle.Version.Frozen {t.Fatal("failed transport changed old roots",err)}
	private:=storage.RowVersion{Row:1,Version:4,PrivateHistory:root}
	next:=storage.RetainHeapPredecessor(private)
	if next.Version.Version!=private.Version || next.Version.PrivateHistory!=nil || next.Next!=root || private.PrivateHistory!=root {t.Fatal("private transition copied or changed retained history")}
}
