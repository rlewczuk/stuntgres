package statistics_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

func TestPGStatXactUserFunctionsPendingProvider(t *testing.T) {
	for _, name := range []string{"twice", "same", "distinct", "mutual", "prefix", "failed_outer", "caught_child", "reset", "retire"} {
		t.Run(name, func(t *testing.T) {
			var p statistics.Provider
			var w, peer statistics.Writer
			f, g := statsKeys()
			f.Category, g.Category = statistics.Function, statistics.Function
			f.Object.Address.ClassID, g.Object.Address.ClassID = ids.ProcedureClassID, ids.ProcedureClassID
			if _, ok := w.PendingFunction(&p, f); ok { t.Fatal("absent key fabricated") }
			begin := func(k statistics.Key) statistics.FunctionUsage {
				u, ok := w.BeginFunction(&p, k)
				if !ok { t.Fatal("live function rejected") }
				return u
			}
			end := func(u statistics.FunctionUsage) { w.EndFunction(&p, u, time.Millisecond) }
			if name == "prefix" { end(begin(f)) }
			outer := begin(f)
			if got, ok := w.PendingFunction(&p, f); !ok || name != "prefix" && (got.Calls != 0 || got.FunctionLogicalTotal != 0 || got.FunctionLogicalSelf != 0) { t.Fatal("admitted zero presence", got, ok) }
			if _, ok := peer.PendingFunction(&p, f); ok { t.Fatal("peer pending leaked") }
			innerKey := f
			if name == "distinct" || name == "mutual" || name == "caught_child" { innerKey = g }
			inner := begin(innerKey)
			if name == "mutual" { end(begin(f)) }
			if name != "caught_child" { end(inner) }
			if name == "reset" { p.Reset(time.Now()) }
			if name == "retire" { peer.Retire(&p, f) }
			if name != "failed_outer" { end(outer) }
			if name == "twice" {
				// Two non-overlapping invocations have the same own cost as recursion.
				p.Reset(time.Now()); end(begin(f)); end(begin(f))
			}
			calls, total, self := int64(2), int64(2), int64(2)
			switch name {
			case "distinct": calls, self = 1, 1
			case "mutual": total = 3
			case "prefix": calls, total, self = 3, 3, 3
			case "failed_outer", "caught_child": calls, total, self = 1, 1, 1
			case "reset", "retire": calls, total, self = 0, 0, 0
			}
			got, present := w.PendingFunction(&p, f)
			if got.Calls != calls || got.FunctionLogicalTotal != total || got.FunctionLogicalSelf != self || present != (name != "reset" && name != "retire") { t.Fatal("logical pending", got, present, calls, total, self) }
			if name == "caught_child" { if zero, ok := w.PendingFunction(&p, g); !ok || zero.Calls != 0 { t.Fatal("failed child's admitted zero", zero, ok) } }
			if name == "distinct" || name == "mutual" {
				child, ok := w.PendingFunction(&p, g); want := int64(1); if name == "mutual" { want = 2 }
				if !ok || child.Calls != 1 || child.FunctionLogicalTotal != want || child.FunctionLogicalSelf != 1 { t.Fatal("inclusive child", child, ok) }
			}
			if n := testing.AllocsPerRun(100, func() { w.PendingFunction(&p, f) }); n != 0 { t.Fatal("inquiry allocation", n) }
			w.Flush(&p, time.Now(), true)
			if _, ok := w.PendingFunction(&p, f); ok { t.Fatal("published pending retained") }
			published := p.Read(f)
			if published.Calls != calls || published.FunctionLogicalTotal != total || published.FunctionLogicalSelf != self { t.Fatal("logical merge", published) }
		})
	}
	t.Run("throttle_and_fences", func(t *testing.T) {
		var p statistics.Provider
		var w statistics.Writer
		f, other := statsKeys(); f.Category, other.Category = statistics.Function, statistics.Function
		f.Object.Address.ClassID, other.Object.Address.ClassID = ids.ProcedureClassID, ids.ProcedureClassID
		at := time.Now()
		u, _ := w.BeginFunction(&p, f)
		if !w.Flush(&p, at, false) { t.Fatal("zero admission not published") }
		if _, ok := w.PendingFunction(&p, f); ok { t.Fatal("zero pending survived publication") }
		u, _ = w.BeginFunction(&p, f); w.EndFunction(&p, u, time.Millisecond)
		for range 3 { w.PendingFunction(&p, f) }
		if w.Flush(&p, at.Add(time.Millisecond), false) { t.Fatal("inquiry moved throttle") }
		if got, ok := w.PendingFunction(&p, f); !ok || got.Calls != 1 { t.Fatal("throttled terminal lost pending", got, ok) }
		if _, ok := w.PendingFunction(&p, other); ok { t.Fatal("qualified identity leaked") }
		wrongEpoch:=f;wrongEpoch.Object.Epoch++
		foreign:=f;foreign.Object=new(ids.Domain).Ref(f.Object.Epoch,f.Object.Address)
		for _,key:=range []statistics.Key{wrongEpoch,foreign}{if _,ok:=w.PendingFunction(&p,key);ok{t.Fatal("qualified generation/domain leaked",key)}}
		wrong := f; wrong.Category = statistics.Relation
		if _, ok := w.PendingFunction(&p, wrong); ok { t.Fatal("category leaked") }
		if _, ok := w.BeginFunction(&p, wrong); ok { t.Fatal("non-function admitted") }
		if !w.Flush(&p, at.Add(time.Second), false) { t.Fatal("eligible publication failed") }
		if _, ok := w.PendingFunction(&p, f); ok { t.Fatal("published entry remained") }
		p.Reset(at)
		w.EndFunction(&p, u, time.Millisecond)
		if _, ok := w.PendingFunction(&p, f); ok { t.Fatal("stale usage restored entry") }
	})
}
