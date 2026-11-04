package statistics_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

func TestStatsReaderFunctionPresence(t *testing.T) {
	for _,mode:=range []string{"none","cache","snapshot"}{t.Run(mode,func(t *testing.T){
		var p statistics.Provider;var w statistics.Writer;var r statistics.Reader
		f,g:=statsKeys();f.Category,g.Category=statistics.Function,statistics.Function;f.Object.Address.ClassID,g.Object.Address.ClassID=ids.ProcedureClassID,ids.ProcedureClassID
		at:=time.Unix(123,0);r.Configure(mode,1)
		if got,ok:=r.ReadEntry(&p,f,at);ok||got.Calls!=0{t.Fatal("absent fabricated",got,ok)}
		u,_:=w.BeginFunction(&p,f)
		if shared,ok:=p.ReadEntry(f);!ok||shared.Calls!=0||shared.FunctionLogicalTotal!=0||shared.FunctionLogicalSelf!=0{t.Fatal("shared admission presence",shared,ok)}
		if cached,ok:=r.ReadEntry(&p,f,at);ok!=(mode=="none")||cached.Calls!=0{t.Fatal("admission mixed live presence into negative cut",cached,ok)}
		w.EndFunction(&p,u,time.Millisecond)
		if shared,ok:=p.ReadEntry(f);!ok||shared.Calls!=0||shared.FunctionLogicalTotal!=0||shared.FunctionLogicalSelf!=0{t.Fatal("completion published pending counters",shared,ok)}
		w.Flush(&p,at,true)
		got,ok:=r.ReadEntry(&p,f,at);if ok!=(mode=="none")||got.Calls!=map[bool]int64{true:1,false:0}[mode=="none"]{t.Fatal("negative membership not retained coherently",got,ok)}
		u,_=w.BeginFunction(&p,g);w.EndFunction(&p,u,time.Millisecond);w.Flush(&p,at,true)
		if _,ok:=r.ReadEntry(&p,g,at);ok==(mode=="snapshot"){t.Fatal("full-domain negative capture",mode,ok)}
		r.Clear();first,ok:=r.ReadEntry(&p,f,at);if !ok||first.Calls!=1{t.Fatal("clear did not refresh",first,ok)}
		u,_=w.BeginFunction(&p,f);w.EndFunction(&p,u,time.Millisecond);w.Flush(&p,at,true)
		got,ok=r.ReadEntry(&p,f,at);want:=int64(1);if mode=="none"{want=2};if !ok||got.Calls!=want{t.Fatal("positive counter cut",got,ok,want)}
		p.Reset(at.Add(time.Second));got,ok=r.ReadEntry(&p,f,at);if mode=="none" {if ok||got.Generation!=1||!got.ResetAt.Equal(at.Add(time.Second)){t.Fatal("reset missing metadata",got,ok)}}else if !ok||got!=first{t.Fatal("reset mutated retained cut",got,first,ok)}
		r.Clear();got,ok=r.ReadEntry(&p,f,at);if ok||got.Generation!=1||!got.ResetAt.Equal(at.Add(time.Second)){t.Fatal("clear reset cut",got,ok)}
	})}
}

func TestStatsFunctionPublishedPresence(t *testing.T) {
	var p statistics.Provider;var w statistics.Writer;var r statistics.Reader
	f,g:=statsKeys();f.Category,g.Category=statistics.Function,statistics.Function;f.Object.Address.ClassID,g.Object.Address.ClassID=ids.ProcedureClassID,ids.ProcedureClassID
	at:=time.Unix(123,0);w.BeginFunction(&p,f)
	if shared,ok:=p.ReadEntry(f);!ok||shared.Calls!=0||shared.FunctionLogicalTotal!=0||shared.FunctionLogicalSelf!=0{t.Fatal("first admission did not prepare shared zero",shared,ok)};if !w.Flush(&p,at,true){t.Fatal("zero entry not published")}
	r.Configure("cache",0);zero,ok:=r.ReadEntry(&p,f,at);if !ok||zero.Calls!=0||zero.FunctionLogicalTotal!=0||zero.FunctionLogicalSelf!=0{t.Fatal("failed first invocation lost presence",zero,ok)}
	outer,_:=w.BeginFunction(&p,f);inner,_:=w.BeginFunction(&p,g);w.EndFunction(&p,inner,time.Millisecond);w.EndFunction(&p,outer,2*time.Millisecond);w.Flush(&p,at,true)
	r.Clear();parent,ok:=r.ReadEntry(&p,f,at);child,childOK:=r.ReadEntry(&p,g,at);if !ok||!childOK||parent.Calls!=1||parent.FunctionLogicalTotal!=2||parent.FunctionLogicalSelf!=1||child.Calls!=1||child.FunctionLogicalTotal!=1||child.FunctionLogicalSelf!=1{t.Fatal("published nested vector",parent,child,ok,childOK)}
	late,_:=w.BeginFunction(&p,f);if shared,ok:=p.ReadEntry(f);!ok||shared!=parent{t.Fatal("admission overwrote prior published work",shared,parent,ok)}
	wrong:=f;wrong.Object.Epoch++;foreign:=f;foreign.Object=new(ids.Domain).Ref(f.Object.Epoch,f.Object.Address);for _,key:=range []statistics.Key{wrong,foreign}{if _,ok:=p.ReadEntry(key);ok{t.Fatal("identity qualification lost",key)}}
	p.Retire(f);if _,ok:=p.ReadEntry(f);ok{t.Fatal("retirement retained presence")};if got,ok:=r.ReadEntry(&p,f,at);!ok||got!=parent{t.Fatal("retirement rewrote cached cut",got,ok)}
	r.Clear();if _,ok:=r.ReadEntry(&p,f,at);ok{t.Fatal("clear retained retired entry")}
	w.EndFunction(&p,late,time.Millisecond);w.Flush(&p,at,true)
	if _,ok:=w.BeginFunction(&p,f);ok{t.Fatal("retired identity admitted a new shared zero")}
	if _,ok:=p.ReadEntry(f);ok{t.Fatal("late completion/report resurrected retired identity")}
	p.Reset(at.Add(time.Second))
	if _,ok:=w.BeginFunction(&p,f);ok{t.Fatal("statistics reset discarded retirement fence")}
	if _,ok:=p.ReadEntry(f);ok{t.Fatal("reset/late admission recreated retired identity")}
	var fresh statistics.Provider;var pending statistics.Writer
	for range 2{usage,_:=pending.BeginFunction(&fresh,f);pending.EndFunction(&fresh,usage,time.Millisecond)}
	if local,ok:=pending.PendingFunction(&fresh,f);!ok||local.Calls!=2||local.FunctionLogicalTotal!=2||local.FunctionLogicalSelf!=2{t.Fatal("completed local vector",local,ok)}
	if shared,ok:=fresh.ReadEntry(f);!ok||shared.Calls!=0||shared.FunctionLogicalTotal!=0||shared.FunctionLogicalSelf!=0{t.Fatal("pending work leaked into shared zero",shared,ok)}
	if !pending.Flush(&fresh,at,true){t.Fatal("legitimate report did not drain completed work")}
	if shared,ok:=fresh.ReadEntry(f);!ok||shared.Calls!=2||shared.FunctionLogicalTotal!=2||shared.FunctionLogicalSelf!=2{t.Fatal("reported vector",shared,ok)}
}
