package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestPGShdependInquiryAcquisitionFrontier(t *testing.T) {
	e := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE ROLE sh_frontier_old", "CREATE DATABASE sh_frontier_peer OWNER sh_frontier_old"} {
		enginetest.SessionSQL(t, admin, q)
	}
	peer, err := e.SelectDatabase(t.Context(), "sh_frontier_peer")
	if err != nil {
		t.Fatal(err)
	}
	writer := enginetest.StartSession(t, peer, nil)
	for _, q := range []string{"CREATE TABLE sh_before(a INT)", "ALTER TABLE sh_before OWNER TO sh_frontier_old"} {
		enginetest.SessionSQL(t, writer, q)
	}
	before := enginetest.RoleTableRef(t, peer, "sh_before").Address
	oldOwner := e.Roles().LookupRef("sh_frontier_old")
	peerDB := peer.Catalog.DatabaseAddress().ObjectID
	privatePeer := enginetest.StartSession(t, peer, nil)
	for _, q := range []string{"BEGIN", "CREATE TABLE sh_frontier_private_peer(a INT)", "ALTER TABLE sh_frontier_private_peer OWNER TO sh_frontier_old"} {
		enginetest.SessionSQL(t, privatePeer, q)
	}

	captureInquiry := func(private bool) *core.StatementScope {
		t.Helper()
		reader := enginetest.StartSession(t, e, nil)
		if private {
			for _, q := range []string{"BEGIN", "CREATE TABLE sh_frontier_private_current(a INT)", "ALTER TABLE sh_frontier_private_current OWNER TO sh_frontier_old"} {
				enginetest.SessionSQL(t, reader, q)
			}
		}
		op, err := reader.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
			op.Done()
		})
		e.Mu.Lock()
		err = e.CaptureCommentSnapshotLocked(scope)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return scope
	}
	open := func(scope *core.StatementScope) *exec.Source {
		t.Helper()
		view := scope.Inquiry.CommentCatalog
		source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ShdependOID})
		ref := view.Owner().Domain.Ref(view.Epoch(), source.Address())
		e.Mu.Lock()
		rows, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, source)
		// The production source-opening boundary consumes the mutex.
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(rows.Close)
		return rows
	}
	type edge struct {
		db, class, object, sub, refclass, role uint32
		kind                                   string
	}
	bag := func(rows *exec.Source) map[edge]int {
		t.Helper()
		out := make(map[edge]int)
		for _, row := range rows.Anonymous {
			var values [6]uint32
			for _, i := range []int{0, 1, 2, 4, 5} {
				value, err := row[i].OID()
				if err != nil {
					t.Fatal(err)
				}
				values[i] = value
			}
			sub, err := row[3].Integer()
			if err != nil {
				t.Fatal(err)
			}
			values[3] = uint32(sub)
			kind, err := row[6].Text()
			if err != nil {
				t.Fatal(err)
			}
			out[edge{values[0], values[1], values[2], values[3], values[4], values[5], kind}]++
		}
		return out
	}
	check := func(rows *exec.Source, expected map[edge]int) {
		t.Helper()
		actual := bag(rows)
		if len(actual) != len(expected) {
			t.Fatalf("frontier bag got %v want %v", actual, expected)
		}
		for edge, count := range expected {
			if actual[edge] != count {
				t.Fatalf("frontier bag got %v want %v", actual, expected)
			}
		}
	}
	old := captureInquiry(true)
	privateAddress := old.Inquiry.CommentCatalog.State().NameAddress(ids.NameKey{ClassID: 1259, Namespace: ids.ObjectAddress{ClassID: 2615, ObjectID: 2200}, Name: "sh_frontier_private_current"})
	if privateAddress.ObjectID == 0 || e.Catalog.State.Get(privateAddress) != nil {
		t.Fatal("missing private-current distinguishing authority")
	}
	retainedPeer := peer.Catalog.State
	retainedRoles := core.InquiryDeparseAuth(&old.Inquiry).RoleRecord
	// Publish after the inquiry has released the common mutex but before any
	// pg_shdepend source opens: the old role root cannot resolve this new owner.
	for _, q := range []string{"CREATE ROLE sh_frontier_new", "CREATE TABLE sh_between(a INT)", "ALTER TABLE sh_between OWNER TO sh_frontier_new"} {
		enginetest.SessionSQL(t, writer, q)
	}
	enginetest.SessionSQL(t, admin, "CREATE DATABASE sh_frontier_later OWNER sh_frontier_new")
	newOwner := e.Roles().LookupRef("sh_frontier_new")
	if peer.Catalog.State == retainedPeer {
		t.Fatal("peer publication did not replace its catalog root")
	}
	if e.RoleStore.Published == retainedRoles {
		t.Fatal("role publication did not replace its authority record")
	}
	// ValidReference checks domain/nonzero identity, not presence in a retained
	// role root. Descriptor is the classifier's actual stale-principal boundary.
	if _, err := core.InquiryDeparseAuth(&old.Inquiry).Roles.Descriptor(newOwner); err == nil || err.Error() != "stuntgres: stale principal" {
		t.Fatal("new peer owner must be absent from retained role authority", err)
	}
	oldBag := map[edge]int{
		{0, 1262, uint32(peerDB), 0, 1260, uint32(oldOwner.ID()), "o"}:                                                             1,
		{uint32(peerDB), 1259, uint32(before.ObjectID), 0, 1260, uint32(oldOwner.ID()), "o"}:                                       1,
		{uint32(e.Catalog.DatabaseAddress().ObjectID), 1259, uint32(privateAddress.ObjectID), 0, 1260, uint32(oldOwner.ID()), "o"}: 1,
	}
	first := open(old)
	check(first, oldBag)
	for _, q := range []string{"CREATE TABLE sh_after(a INT)", "ALTER TABLE sh_after OWNER TO sh_frontier_new"} {
		enginetest.SessionSQL(t, writer, q)
	}
	second := open(old)
	check(second, oldBag)
	check(first, oldBag)
	between := enginetest.RoleTableRef(t, peer, "sh_between").Address
	after := enginetest.RoleTableRef(t, peer, "sh_after").Address
	later, err := e.SelectDatabase(t.Context(), "sh_frontier_later")
	if err != nil {
		t.Fatal(err)
	}
	freshBag := make(map[edge]int)
	for edge, count := range oldBag {
		freshBag[edge] = count
	}
	delete(freshBag, edge{uint32(e.Catalog.DatabaseAddress().ObjectID), 1259, uint32(privateAddress.ObjectID), 0, 1260, uint32(oldOwner.ID()), "o"})
	freshBag[edge{0, 1262, uint32(later.Catalog.DatabaseAddress().ObjectID), 0, 1260, uint32(newOwner.ID()), "o"}] = 1
	freshBag[edge{uint32(peerDB), 1259, uint32(between.ObjectID), 0, 1260, uint32(newOwner.ID()), "o"}] = 1
	freshBag[edge{uint32(peerDB), 1259, uint32(after.ObjectID), 0, 1260, uint32(newOwner.ID()), "o"}] = 1
	check(open(captureInquiry(false)), freshBag)
	check(second, oldBag)
}
