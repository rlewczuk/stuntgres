package statistics_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/util"
)

func TestPGStatXactAllTablesPendingProvider(t *testing.T) {
	var p statistics.Provider
	var writer,peer statistics.Writer
	a,b:=statsKeys();now:=time.Unix(100,0)
	want:=statistics.Fact{Inserts:2,Updates:3,Deletes:4,SeqScans:5,SeqTuples:6,IndexScans:7,IndexFetches:8,TableIndexFetches:9}
	check:=func(key statistics.Key,want statistics.Fact){t.Helper();if got:=writer.PendingRelation(&p,key);got!=want{t.Fatal("qualified pending fact",key,got,want)}}
	check(a,statistics.Fact{})
	allocations,bytes:=util.AllocationsPerCall(100,func(){writer.PendingRelation(&p,a)});if allocations!=0||bytes!=0{t.Fatal("absent inquiry allocated",allocations,bytes)}
	writer.Add(&p,a,want);retained:=writer.PendingRelation(&p,a)
	peer.Add(&p,a,statistics.Fact{Inserts:11});peer.Flush(&p,now,true)
	check(a,want);check(b,statistics.Fact{})
	foreign,next,category:=a,a,a;foreign.Object=(&ids.Domain{}).Ref(a.Object.Epoch,a.Object.Address);next.Object.Epoch++;category.Category=statistics.Function
	writer.Add(&p,category,statistics.Fact{Inserts:99});check(category,statistics.Fact{});check(foreign,statistics.Fact{});check(next,statistics.Fact{})
	allocations,bytes=util.AllocationsPerCall(100,func(){writer.PendingRelation(&p,a)});if allocations!=0||bytes!=0{t.Fatal("populated inquiry allocated",allocations,bytes)}
	for i,mode:=range []string{"none","cache","snapshot"}{var reader statistics.Reader;reader.Configure(mode,uint64(i));if f:=reader.Read(&p,a,now);f.Inserts!=11{t.Fatal("published peer fact",f)};reader.Clear();check(a,want)}
	if !writer.Flush(&p,now,false){t.Fatal("inquiry moved first flush frontier")};check(a,statistics.Fact{})
	writer.Add(&p,a,statistics.Fact{Inserts:1});writer.TransactionStopped(now.Add(500*time.Millisecond))
	if writer.Flush(&p,now.Add(999*time.Millisecond),false){t.Fatal("terminal stop bypassed throttle")};check(a,statistics.Fact{Inserts:1})
	p.ResetShared(statistics.SharedIO,now);check(a,statistics.Fact{Inserts:1})
	if !writer.Flush(&p,now.Add(time.Second),false){t.Fatal("eligible batch blocked")};check(a,statistics.Fact{})
	if retained!=want{t.Fatal("copied pending fact mutated",retained)}
	writer.Add(&p,a,want);p.Retire(a);check(a,statistics.Fact{});writer.Flush(&p,now,true);if p.Read(a)!=(statistics.Fact{}){t.Fatal("retired batch resurrected")}
	writer.Add(&p,b,want);p.Reset(now);check(b,statistics.Fact{});writer.Flush(&p,now,true);if p.Read(b).Inserts!=0{t.Fatal("reset accepted stale batch")}
	writer.Add(&p,b,want);p.Replace(now,map[statistics.Key]statistics.Fact{next:{Inserts:29}});check(b,statistics.Fact{});writer.Flush(&p,now,true);if p.Read(next).Inserts!=29||p.Read(b).Inserts!=0{t.Fatal("replacement crossed generation")}
	writer.Add(&p,next,statistics.Fact{Inserts:1});check(next,statistics.Fact{Inserts:1});check(a,statistics.Fact{});check(foreign,statistics.Fact{})
}
