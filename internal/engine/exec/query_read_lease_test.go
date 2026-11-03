package exec

import (
	"context"
	"testing"
)

type bufferReadLeaseProbe struct{ opens, closes int }

func (p *bufferReadLeaseProbe) Open()  { p.opens++ }
func (p *bufferReadLeaseProbe) Close() { p.closes++ }

func TestPgBuffercacheMixedHierarchyReadFrontier(t *testing.T) {
	f := newHierarchyFixture(t, nil)
	local, remote := &bufferReadLeaseProbe{}, &bufferReadLeaseProbe{}
	f.local.ReadLease, f.foreign.ReadLease = local, remote
	stage, err := OpenNode(context.Background(), f.state, f.node)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if local.opens != 0 || remote.opens != 0 {
		t.Fatal("opening hierarchy heated an unreached prefix")
	}
	hierarchyNext(t, &stage)
	if local.opens != 1 || remote.opens != 0 {
		t.Fatal("physical prefix did not open exactly once", local, remote)
	}
	hierarchyNext(t, &stage)
	if local.opens != 1 || remote.opens != 0 {
		t.Fatal("foreign prefix acquired a physical read", local, remote)
	}
	if _, err := stage.Rewind(context.Background()); err != nil {
		t.Fatal(err)
	}
	hierarchyNext(t, &stage)
	if local.opens != 1 {
		t.Fatal("rewinding one stage heated physical storage again", local.opens)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := OpenNode(context.Background(), f.state, f.node)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	hierarchyNext(t, &fresh)
	if local.opens != 2 || remote.opens != 0 {
		t.Fatal("fresh actual invocation lost its read", local, remote)
	}
}
