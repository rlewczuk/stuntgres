package enginetests

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestRelationSizeAllocation(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() { types[typ.ID()] = typ }
	i,err := sqltype.Integer(types[sqltype.Int4],1)
	if err != nil { t.Fatal(err) }
	x := sqltype.String(types[sqltype.Text],"x")
	b := sqltype.Boolean(types[sqltype.Bool],true)
	domain,err:=sqltype.Domain(49001,"sized","public.sized",types[sqltype.Int4].Usage())
	if err!=nil {t.Fatal(err)}
	wrapped,err:=domain.Wrap(i)
	if err!=nil {t.Fatal(err)}
	if n,err:=storage.RowAllocation(context.Background(),[]sqltype.Value{wrapped});err!=nil||n!=40{t.Fatal("domain base width",n,err)}
	rows := []storage.RowVersion{{Row:1,Version:1,Values:[]sqltype.Value{i,x,b}},{Row:2,Version:2,Values:[]sqltype.Value{i,sqltype.Null(types[sqltype.Text]),b}}}
	heap := storage.Allocation{Relation:&catalog.Relation{Attributes:[]int32{1,2,3}},Allocated:true,Versions:rows}
	forks,err := heap.Forks(context.Background())
	if err != nil || forks!=(storage.ForkSizes{Main:192,FSM:8,VM:1}) { t.Fatal(forks,err) }
	total,err := forks.Total()
	if err != nil || total!=201 { t.Fatal(total,err) }
	idx := storage.Allocation{Relation:&catalog.Relation{SQLIndex:&catalog.IndexDefinition{}},Allocated:true,Index:&storage.IndexRoot{Entries:[]storage.IndexEntry{{Keys:[]sqltype.Value{i}},{Keys:[]sqltype.Value{i}}}}}
	indexForks,err := idx.Forks(context.Background())
	nulls := make([]sqltype.Value,65)
	for j := range nulls { nulls[j] = sqltype.Null(types[sqltype.Int4]) }
	if err != nil || indexForks.Main!=112 || indexForks.FSM!=0 || indexForks.VM!=0 || indexForks.Init!=0 { t.Fatal(indexForks,err) }
	combined,err := storage.AddSize(total,indexForks.Main)
	if err != nil || combined!=313 { t.Fatal(combined,err) }
	for _, tc := range []struct{ values []sqltype.Value; want int64 }{
		{nil,24},{[]sqltype.Value{i},40},{[]sqltype.Value{sqltype.Null(types[sqltype.Int4])},32},
		{[]sqltype.Value{x},64},{[]sqltype.Value{sqltype.String(types[sqltype.Text],"much longer payload")},64},
		{nulls,40},
	} {
		n,err := storage.RowAllocation(context.Background(),tc.values)
		if err != nil || n!=tc.want { t.Fatal(n,tc.want,err) }
	}
	for _, n := range []int{0,8,9,256,257} {
		heap.Versions = make([]storage.RowVersion,n)
		got,err := heap.Forks(context.Background())
		if err != nil || got.Main!=64+24*int64(n) || got.FSM!=int64((n+255)/256*8) || got.VM!=int64((n+7)/8) { t.Fatal(n,got,err) }
	}
	heap.Allocated = false
	if got,err := heap.Forks(context.Background()); err != nil || got!=(storage.ForkSizes{}) { t.Fatal(got,err) }
	for _, persistence := range []string{"logged","temporary","unlogged"} {
		seq := storage.Allocation{Allocated:true,Relation:&catalog.Relation{Sequence:&catalog.SequenceOptions{Persistence:persistence}}}
		got,err := seq.Forks(context.Background())
		init := int64(0)
		if persistence=="unlogged" { init=64 }
		if err != nil || got!=(storage.ForkSizes{Main:64,Init:init}) { t.Fatal(persistence,got,err) }
	}
	if _,err := storage.AddSize(math.MaxInt64,1); err==nil { t.Fatal("overflow accepted") }
	if _,err := (storage.ForkSizes{Main:math.MaxInt64,VM:1}).Total(); err==nil { t.Fatal("component overflow accepted") }
	ctx,cancel := context.WithCancel(context.Background()); cancel()
	if _,err := heap.Forks(ctx); !errors.Is(err,context.Canceled) { t.Fatal(err) }
	active,stop:=context.WithCancel(context.Background())
	defer stop()
	interrupt:=&allocationCancelContext{Context:active,cancel:stop,polls:3}
	heap.Allocated=true
	heap.Versions=make([]storage.RowVersion,1024)
	if _,err:=heap.Forks(interrupt);!errors.Is(err,context.Canceled){t.Fatal("in-flight cancellation",err)}
	if len(heap.Versions)!=1024{t.Fatal("cancellation mutated borrowed vector")}
}

func TestRelationSizeIndexAllocation(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() { types[typ.ID()] = typ }
	i,_ := sqltype.Integer(types[sqltype.Int4],7)
	x := sqltype.String(types[sqltype.Text],"included")
	parent := &catalog.Relation{Attributes:[]int32{2,7}}
	row := storage.RowVersion{Row:9,Version:11,Values:[]sqltype.Value{i,x}}
	entry := storage.IndexEntry{Row:9,Version:11,Position:0,Keys:[]sqltype.Value{i}}
	idx := &catalog.Relation{ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:90000},SQLIndex:&catalog.IndexDefinition{Keys:[]catalog.IndexKey{{Attribute:2}},Include:[]int32{7}}}
	a := storage.Allocation{Relation:idx,Allocated:true,Parent:parent,ParentVersions:[]storage.RowVersion{row},Index:&storage.IndexRoot{Entries:[]storage.IndexEntry{entry}}}
	got,err := a.Forks(context.Background())
	if err != nil || got.Main!=120 { t.Fatal("include",got,err) }
	// Same physical row referenced by a private root and own vector is not
	// allocated twice. Predicates/expressions are never evaluated by sizing.
	a.ParentOwn = []storage.RowVersion{row}
	got,err = a.Forks(context.Background())
	if err != nil || got.Main!=120 { t.Fatal("private duplicate",got,err) }
	a.Index=&storage.IndexRoot{}
	a.ParentStaged=[]storage.RowVersion{row}
	got,err=a.Forks(context.Background())
	if err!=nil||got.Main!=120{t.Fatal("nested own/staged version counted once",got,err)}
	a.ParentStaged=nil
	a.ParentOwn=nil
	a.Index=&storage.IndexRoot{Hash:&storage.HashRoot{Buckets:map[uint64][]storage.HashEntry{4:{{Key:i,Row:9,Version:11}}},RowEntries:map[ids.RowID]*storage.HashEntry{9:{Key:i}}}}
	got,err = a.Forks(context.Background())
	if err != nil || got.Main!=88 { t.Fatal("hash buckets once",got,err) }
	a.Index=&storage.IndexRoot{Brin:&storage.BrinRoot{Ranges:[]storage.BrinRange{{Summarized:false,Keys:[]storage.BrinSummary{{HasNonNull:true,Min:i,Max:i}}},{Summarized:true,Keys:[]storage.BrinSummary{{HasNonNull:true,Min:i,Max:i},{HasNull:true}}}}}}
	got,err = a.Forks(context.Background())
	if err != nil || got.Main!=112 { t.Fatal("summaries only",got,err) }
	a.Index=&storage.IndexRoot{}
	got,err = a.Forks(context.Background())
	if err != nil || got.Main!=64 { t.Fatal("empty index",got,err) }
	a.Index=nil
	got,err = a.Forks(context.Background())
	if err != nil || got.Main!=0 { t.Fatal("absent index",got,err) }
}

// Cancel synchronously at an arithmetic poll, without timers or scheduler races.
type allocationCancelContext struct {
	context.Context
	cancel context.CancelFunc
	polls int
}

func (c *allocationCancelContext) Err() error {
	c.polls--
	if c.polls==0 {c.cancel()}
	return c.Context.Err()
}
