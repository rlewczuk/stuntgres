package engine

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGEnumAllocatorBounds(t *testing.T) {
	e := New()
	for _, want := range []int64{1, 3} {
		got, err := e.allocateEnumMemberLocked()
		if err != nil || got != want {
			t.Fatal("initial allocation", got, want, err)
		}
	}
	e.nextEnumMember = math.MaxUint32 - 2
	if got, err := e.allocateEnumMemberLocked(); err != nil || got != math.MaxUint32 {
		t.Fatal("last OID", got, err)
	}
	for range 2 {
		got, err := e.allocateEnumMemberLocked()
		if got != 0 || err == nil || err.Error() != "stuntgres: enum member identity exhausted" || e.nextEnumMember != math.MaxUint32 {
			t.Fatal("exhaustion wrapped/aliased", got, err, e.nextEnumMember)
		}
	}
	// Discard an actual CREATE candidate: rollback cannot recycle reservations.
	e = New()
	op, done := roleOperation(t, e, "postgres")
	stage := domainUnitStage(t, e, op, "CREATE TYPE abandoned AS ENUM ('a','b')")
	e.DiscardCatalogStage(stage)
	done()
	if got, err := e.allocateEnumMemberLocked(); err != nil || got != 5 {
		t.Fatal("discard recycled reservation", got, err)
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err := e.allocateEnumMemberLocked(); err != nil || got != 7 {
		t.Fatal("Reset recycled reservation", got, err)
	}
	op, done = roleOperation(t, e, "postgres")
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE restored AS ENUM ('a')"))
	done()
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	frontier := e.nextEnumMember
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if e.nextEnumMember <= frontier {
		t.Fatal("Restore reused reservations", frontier, e.nextEnumMember)
	}
	last := e.nextEnumMember
	if got, err := e.allocateEnumMemberLocked(); err != nil || got != last+2 {
		t.Fatal("post-Restore frontier", got, last, err)
	}
}

func TestPGEnumSortInsertion(t *testing.T) {
	if got := enumInsertionOrder(nil, 0); got != 1 {
		t.Fatal("empty", got)
	}
	members := []sqltype.EnumMember{{ID: 91, Label: "a", SortOrder: 1}, {ID: 21, Label: "b", SortOrder: 2}}
	for _, tc := range []struct {
		at   int
		want float32
	}{{0, 0}, {1, 1.5}, {2, 3}} {
		if got := enumInsertionOrder(members, tc.at); got != tc.want {
			t.Fatal("insertion", tc, got)
		}
	}
	dense := []sqltype.EnumMember{{ID: 91, Label: "a", SortOrder: 1}, {ID: 21, Label: "b", SortOrder: math.Nextafter32(1, 2)}}
	codec, err := sqltype.EnumWithMembers(45000, "dense", "public.dense", dense)
	if err != nil {
		t.Fatal(err)
	}
	if got := enumInsertionOrder(dense, 1); got != 1.5 || dense[0].SortOrder != 1 || dense[1].SortOrder != 2 || dense[0].ID != 91 || dense[1].ID != 21 {
		t.Fatal("collapse must renumber and retry without reminting", got, dense)
	}
	old, _ := codec.EnumMemberAt(1)
	if old.SortOrder != math.Nextafter32(1, 2) {
		t.Fatal("renumber mutated previous generation", old)
	}
	negative := []sqltype.EnumMember{{ID: 1, Label: "a", SortOrder: 0}}
	if got := enumInsertionOrder(negative, 0); got != -1 {
		t.Fatal("negative prepend", got)
	}
}

func pgEnumUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3501}
	d, ok := e.state.descriptors[a].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_enum")
	}
	return d, e.domain.Ref(e.state.epoch, a)
}

func TestPGEnumShapeAndCapture(t *testing.T) {
	e := New()
	d, ref := pgEnumUnitSource(t, e)
	if d.name != "pg_enum" || d.namespace.ObjectID != 11 || d.relationKind != catalogRelation || d.rowType != 10045 || d.attributeHighWater != 4 || len(d.columns) != 4 {
		t.Fatal("source descriptor", d)
	}
	for i, id := range [4]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Float4, sqltype.Name} {
		c := d.columns[i]
		owned, ok := e.CatalogRead().LookupType(id)
		coll := uint32(0)
		if i == 3 {
			coll = 950
		}
		if !ok || c.Name != []string{"oid", "enumtypid", "enumsortorder", "enumlabel"}[i] || c.Usage.Type != owned || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != coll || c.Nullable || !c.NullabilityKnown || c.OriginTable != 3501 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[c.Name] != i {
			t.Fatal("source column", i, c)
		}
	}
	row := e.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10045}].(*typeDescriptor)
	array := e.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10044}].(*typeDescriptor)
	if row.owningRelation != d.address || array.owningRelation != d.address || row.array != 10044 || row.owner != d.owner || array.owner != d.owner {
		t.Fatal("row/array ownership", row, array)
	}
	if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	captured, err := capturePGEnum(context.Background(), e.CatalogRead(), ref, d)
	if err != nil || captured == nil || captured.ref != ref || captured.anonymous == nil || len(captured.anonymous) != 0 {
		t.Fatal("fresh empty source", captured, err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	readerRole := roleAttributeCreate(t, e, op, "enum_metadata_reader", true, parser.RoleOptions{})
	readerOp, readerDone := roleOperation(t, e, readerRole.Name)
	defer readerDone()
	retainedAuth, err := readerOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := retainedAuth.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
		t.Fatal("PUBLIC catalog ACL", err)
	}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	currentAuth, err := readerOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, currentAuth.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}), "42501", "permission denied for table pg_enum")
	if err := retainedAuth.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
		t.Fatal("ACL update mutated retained authorization", err)
	}
	if err := e.SetObjectACL(context.Background(), ref, d.acl); err != nil {
		t.Fatal(err)
	}
	restoredAuth, err := readerOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := restoredAuth.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
		t.Fatal("restored PUBLIC catalog ACL", err)
	}
	domainUnitDiagnostic(t, currentAuth.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}), "42501", "permission denied for table pg_enum")
	for _, q := range []string{"CREATE TYPE mood AS ENUM ('z','q')", "CREATE TYPE empty AS ENUM ()", "CREATE DOMAIN wrapped AS mood"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	mood := domainUnitType(t, e.CatalogRead(), "mood")
	captured, err = e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.CatalogRead(), e.roles), ref, d)
	if err != nil || len(captured.anonymous) != 2 {
		t.Fatal("enum-only capture", captured, err)
	}
	before := append([][]sqltype.Value(nil), captured.anonymous...)
	for _, values := range captured.anonymous {
		for i, v := range values {
			if v.IsNull() || v.Type() != d.columns[i].Usage.Type {
				t.Fatal("unowned capture cell", i, v)
			}
		}
		memberID, _ := values[0].OID()
		owner, _ := values[1].OID()
		order, _ := values[2].Float()
		label, _ := values[3].Text()
		matched := false
		for i := range mood.typ.EnumMemberCount() {
			m, _ := mood.typ.EnumMemberAt(i)
			if uint32(m.ID) == memberID && m.Label == label && float64(m.SortOrder) == order && owner == uint32(mood.typ.ID()) {
				matched = true
			}
		}
		if !matched {
			t.Fatal("row is not actual owner metadata", values)
		}
	}
	members := mood.typ.EnumMembers()
	members[1].Label = "renamed"
	typ, err := sqltype.EnumWithMembers(mood.typ.ID(), mood.Name(), mood.typ.SQLName(), members)
	if err != nil {
		t.Fatal(err)
	}
	next := *mood
	next.typ, next.revision = typ, mood.revision+1
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	private, err := capturePGEnum(context.Background(), e.stageRead(stage), ref, d)
	if err != nil || reflect.DeepEqual(private.anonymous, before) || !reflect.DeepEqual(captured.anonymous, before) {
		t.Fatal("capture generation lifetime", err)
	}
	invalid := *d
	invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
	invalid.columns[0].Usage.Type, _ = New().CatalogRead().LookupType(sqltype.Oid)
	if rows, err := capturePGEnum(context.Background(), e.CatalogRead(), ref, &invalid); err == nil || rows != nil {
		t.Fatal("foreign source type", rows, err)
	}
	invalid.columns = invalid.columns[:3]
	if rows, err := capturePGEnum(context.Background(), e.CatalogRead(), ref, &invalid); err == nil || rows != nil {
		t.Fatal("invalid shape", rows, err)
	}
	invalidOwner := *e.state
	invalidOwner.descriptors = make(map[ids.ObjectAddress]Descriptor, len(e.state.descriptors))
	for address, descriptor := range e.state.descriptors {
		invalidOwner.descriptors[address] = descriptor
	}
	delete(invalidOwner.descriptors, mood.Address())
	wrongAddress := mood.Address()
	wrongAddress.ObjectID++
	invalidOwner.descriptors[wrongAddress] = mood
	if rows, err := capturePGEnum(context.Background(), CatalogRead{owner: e, state: &invalidOwner}, ref, d); err == nil || rows != nil {
		t.Fatal("mismatched owner identity", rows, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := capturePGEnum(canceled, e.CatalogRead(), ref, d); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("canceled capture", rows, err)
	}
	during := &pgEnumCancelDuringCapture{Context: context.Background(), after: len(e.state.descriptors) + 2}
	if rows, err := capturePGEnum(during, e.CatalogRead(), ref, d); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("traversal cancellation", rows, err)
	}
}

type pgEnumCancelDuringCapture struct {
	context.Context
	calls, after int
}

func (c *pgEnumCancelDuringCapture) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}
