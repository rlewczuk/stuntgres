package enginetests

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

type clusterBindingProgress struct {
	started,completed []ids.ObjectAddress
}
func (*clusterBindingProgress) ScanStart(int,catalog.IndexMethod) {}
func (*clusterBindingProgress) ScanDone(int) {}
func (*clusterBindingProgress) ScanFinish(int,catalog.IndexMethod) {}
func (p *clusterBindingProgress) IndexStart(_ ids.ObjectAddress,a ids.ObjectAddress) { p.started = append(p.started,a) }
func (p *clusterBindingProgress) IndexComplete(a ids.ObjectAddress) { p.completed = append(p.completed,a) }

func TestClusterProgressCompletedBuilds(t *testing.T) {
	a := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:90001}
	other := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:90002}
	for _,many := range []bool{false,true} {
		p := &clusterBindingProgress{}
		b := index.ProgressBinding{Index:a,Observer:p}
		if many { b.Targets = map[ids.ObjectAddress]ids.ObjectAddress{a:other} }
		if b.ObserverFor(other)!=nil { t.Fatal("unselected index acquired observer") }
		b.Complete(other)
		if len(p.started)!=0 || len(p.completed)!=0 { t.Fatal("unselected index notified completion",p) }
		observer := b.ObserverFor(a)
		starts := 0
		if many { starts=1 }
		if observer==nil || len(p.started)!=starts { t.Fatal("selected actual start",p) }
		if many && p.started[0]!=a { t.Fatal("wrong target start",p) }
		observer.ScanStart(3,catalog.BtreeMethod)
		observer.ScanDone(3)
		observer.ScanFinish(3,catalog.BtreeMethod)
		if len(p.completed)!=0 { t.Fatal("scan finish claimed successful attachment") }
		b.Complete(a)
		if len(p.started)!=starts || len(p.completed)!=1 || p.completed[0]!=a { t.Fatal("completion must not restart the index",p) }
	}
	// Optional completion leaves existing scan-only bindings valid.
	var empty index.ProgressBinding
	empty.Complete(a)
}
