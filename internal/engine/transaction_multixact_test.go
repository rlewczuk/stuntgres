package engine

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func identityMultiUnitTuple(e *core.Engine, row ids.RowID) lock.MultiTuple {
	resource := blockingUnitResource(e)
	resource.Row = row
	return lock.MultiTuple{resource, ids.VersionID(row)}
}

func identityMultiGrant(t *testing.T, e *core.Engine, owner *lock.Ticket, tuple lock.MultiTuple, mode lock.Mode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := e.Locks.StartAcquire(ctx, owner, tuple.Resource, mode, lock.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if request != nil {
		if err := request.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func identityMultiEmit(t *testing.T, e *core.Engine, owner *lock.Ticket, tuple lock.MultiTuple, mode lock.MultiMode) {
	t.Helper()
	if err := e.Locks.PhysicalEvents(owner, []lock.MultiEvent{{Tuple: tuple, Mode: mode}}); err != nil {
		t.Fatal(err)
	}
}

func identityMultiWant(t *testing.T, e *core.Engine, id uint32, want ...string) {
	t.Helper()
	members, err := e.Locks.MultiXactMembers(e.Catalog.Domain, nil, id)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(members))
	for i, m := range members {
		got[i] = fmt.Sprintf("%d/%s", m.XID, m.Mode.Name())
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MultiXact %d: %v want %v", id, got, want)
	}
	if len(members) != 0 {
		members[0].XID = 0
		again, err := e.Locks.MultiXactMembers(e.Catalog.Domain, nil, id)
		if err != nil || again[0].XID == 0 {
			t.Fatal("borrowed mutable history", err)
		}
	}
}
