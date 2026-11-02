package exec

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQuerySystemAttributePhysicalFrames(t *testing.T) {
	domain := new(ids.Domain)
	parent := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24000})
	child := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24001})
	// Equal logical RowID does not make these two genuine versions interchangeable.
	versions := []storage.RowVersion{{Row: 7, Version: 9, InsertFullXID: 41}, {Row: 7, Version: 65536, InsertFullXID: 1<<32 | 42, DeleteFullXID: 2<<32 | 43}}
	prefix := &Source{Ref: child, Committed: versions, Visible: []int{1}}
	source := &Source{Ref: parent, Occurrence: 7, Inheritance: []InheritanceQuerySegment{{Leaf: Leaf{Ref: child}, Prefix: prefix}}, InheritanceEnds: []int{1}}
	outer := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 4, Query: &State{Source: source, IdentityReads: true}, QueryOccurrences: []int{7}, QueryProvenance: []int{0}}}
	inner := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 8, Outer: &outer}}
	for _, name := range []string{"ctid", "xmin", "xmax", "tableoid"} {
		subID, typeID := catalog.SystemAttribute(name)
		typ := asyncUnitType(t, typeID)
		n := plan.SystemAttributeExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: parent, SubID: subID, Scope: 1, Frame: 4, QueryOccurrence: 1}
		value, err := n.Eval(&inner)
		if err != nil {
			t.Fatal(name, err)
		}
		switch subID {
		case -1:
			block, offset, err := value.TID()
			if err != nil || block != 1 || offset != 1 {
				t.Fatal("ctid used RowID, ordinal or unselected version", block, offset, err)
			}
		case -2, -4:
			want := uint32(42)
			if subID == -4 {
				want = 43
			}
			if got, err := value.XID(); err != nil || got != want {
				t.Fatal("header is not low-32-bit typed XID", name, got, err)
			}
		case -6:
			if got, err := value.OID(); err != nil || got != uint32(child.Address.ObjectID) {
				t.Fatal("lost physical child", got, err)
			}
		}
		outer.QueryProvenance[0] = AbsentQueryIndex
		absent, err := n.Eval(&inner)
		if err != nil || !absent.IsNull() || absent.Type() != typ {
			t.Fatal("zero-column outer absence", name, absent, err)
		}
		outer.QueryProvenance[0] = 0
	}
	// EPQ must read the actual locked successor, not the pre-wait capture.
	outer.QueryCurrentVersions = []storage.RowVersion{{Row: 7, Version: 131071, InsertFullXID: 44}}
	tid := asyncUnitType(t, sqltype.Tid)
	n := plan.SystemAttributeExpr{Info: expr.Info{Usage: tid.Usage()}, Owner: parent, SubID: -1, Scope: 1, Frame: 4, QueryOccurrence: 1}
	value, err := n.Eval(&inner)
	block, offset, tidErr := value.TID()
	if err != nil || tidErr != nil || block != 2 || offset != 1 {
		t.Fatal("recheck used captured predecessor", block, offset, err, tidErr)
	}
	// A completed rowmark frontier projects its actual cached version, while
	// each frame keeps a detached immutable raw-header publication.
	current := storage.RowVersion{Row: 7, Version: 131072, InsertFullXID: 45}
	captured := &Source{Ref: child, Committed: []storage.RowVersion{versions[0]}, RawXmax: map[ids.VersionID]uint32{current.Version: 77}}
	projection := &Source{Anonymous: [][]sqltype.Value{{}}, Materialized: true, FlatCount: 1, Occurrences: []int{0}, Provenance: []int{0}, Versions: []storage.RowVersion{current}}
	frame := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 1, Query: &State{Source: captured, IdentityReads: true}}}
	frame.BindQueryIdentity(projection, 0)
	xid := asyncUnitType(t, sqltype.Xid)
	rawExpr := plan.SystemAttributeExpr{Info: expr.Info{Usage: xid.Usage()}, Owner: child, SubID: -4, Scope: 1, Frame: 1, QueryOccurrence: 1}
	freshFrame := frame
	captured.RawXmax = map[ids.VersionID]uint32{current.Version: 88}
	freshFrame.BindQueryIdentity(projection, 0)
	for _, capture := range []struct {
		frame *Context
		xmax  uint32
	}{{&frame, 77}, {&freshFrame, 88}} {
		value, err := rawExpr.Eval(capture.frame)
		got, xidErr := value.XID()
		if err != nil || xidErr != nil || got != capture.xmax {
			t.Fatal("rowmark projection lost immutable raw scalar", got, capture.xmax, err, xidErr)
		}
	}
	if current.DeleteFullXID != 0 || projection.Versions[0].DeleteFullXID != 0 {
		t.Fatal("raw projection contaminated full-XID provenance")
	}
}

func TestQuerySystemAttributeCapturedRepair(t *testing.T) {
	domain := new(ids.Domain)
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24000})
	row := storage.RowVersion{Row: 7, Version: 9, InsertFullXID: 41, DeleteFullXID: 42}
	oldSource := &Source{Ref: ref, Occurrence: 0, Committed: []storage.RowVersion{row}}
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{row})
	if err != nil {
		t.Fatal(err)
	}
	fresh := *oldSource
	fresh.Headers = headers
	for _, capture := range []struct {
		source     *Source
		xmin, xmax uint32
	}{{oldSource, 41, 42}, {&fresh, 2, 0}} {
		c := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 1, Query: &State{Source: capture.source, IdentityReads: true}, QueryOccurrences: []int{0}, QueryProvenance: []int{0}}}
		for _, subID := range []int32{-2, -4} {
			xid := asyncUnitType(t, sqltype.Xid)
			n := plan.SystemAttributeExpr{Info: expr.Info{Usage: xid.Usage()}, Owner: ref, SubID: subID, Scope: 1, Frame: 1, QueryOccurrence: 1}
			value, err := n.Eval(&c)
			want := capture.xmin
			if subID == -4 {
				want = capture.xmax
			}
			if got, xidErr := value.XID(); err != nil || xidErr != nil || got != want {
				t.Fatal("captured header root was mutated or ignored", got, want, err, xidErr)
			}
		}
	}
	if oldSource.Committed[0].ForcedFrozen || oldSource.Committed[0].InsertFullXID != 41 || oldSource.Committed[0].DeleteFullXID != 42 || &fresh.Committed[0] != &oldSource.Committed[0] {
		t.Fatal("repair copied or changed captured version vector")
	}
}

func TestQuerySystemAttributeReturningFrames(t *testing.T) {
	domain := new(ids.Domain)
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 24000})
	old := storage.RowVersion{Row: 7, Version: 9, InsertFullXID: 41, ForcedFrozen: true, DeleteFullXID: 42}
	next := storage.RowVersion{Row: 7, Version: 65536, InsertFullXID: 42}
	c := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 1, PhysicalTable: ref, PhysicalVersion: next, OldPhysicalTable: ref, OldPhysicalVersion: old}}
	tid := asyncUnitType(t, sqltype.Tid)
	n := plan.SystemAttributeExpr{Info: expr.Info{Usage: tid.Usage()}, Owner: ref, SubID: -1, Scope: 1, Frame: 1}
	for _, prior := range []bool{false, true} {
		n.Old = prior
		value, err := n.Eval(&c)
		block, offset, tidErr := value.TID()
		wantBlock, wantOffset := uint32(1), uint16(1)
		if prior {
			wantBlock, wantOffset = 0, 9
		}
		if err != nil || tidErr != nil || block != wantBlock || offset != wantOffset {
			t.Fatal("RETURNING retargeted actual version", prior, block, offset, err, tidErr)
		}
	}
	n.Absent = true
	if value, err := n.Eval(&c); err != nil || !value.IsNull() || value.Type() != tid {
		t.Fatal("absent transition header", value, err)
	}
	physical := QueryPhysicalRow{Leaf: Leaf{Ref: ref}, Version: old}
	deletion := physical.Deletion()
	if deletion.Own || deletion.Version != old.Version || deletion.RepairStamp != old.RepairStamp {
		t.Fatal("repair became private/head deletion", deletion)
	}
}
