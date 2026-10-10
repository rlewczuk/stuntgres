package sqltype

import (
	"context"
	"testing"
)

func TestXIDScalarAuthorityAndGrouping(t *testing.T) {
	ctx := context.Background()
	var xid, oid *Descriptor
	for _, d := range Builtins() {
		if d.ID() == Xid {
			xid = d
		}
		if d.ID() == Oid {
			oid = d
		}
	}
	if xid == nil || xid.ID() != 28 || xid.Name() != "xid" || xid.DatabaseName() != "XID" || xid.Usage().Collation.ID != 0 {
		t.Fatal(xid)
	}
	if _, err := Array(90000, "_boundary", xid); err == nil {
		t.Fatal("narrow XID enabled stored arrays")
	}
	for _, n := range []uint32{0, 1, 4294967295} {
		value, err := XIDValue(xid, n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := value.OID(); err == nil {
			t.Fatal("XID adopted OID authority")
		}
		text, err := xid.Output(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := xid.Input(ctx, text, -1)
		if err != nil || !SameDatum(value, decoded) {
			t.Fatal(text, decoded, err)
		}
		literal, err := xid.SQLLiteral(ctx, value)
		if err != nil || literal != "'"+text+"'" {
			t.Fatal(literal, err)
		}
		group, err := BindGrouping(xid.Usage())
		if err != nil {
			t.Fatal(err)
		}
		equal, err := group.Equal(ctx, value, decoded)
		if err != nil || !equal {
			t.Fatal(equal, err)
		}
		h1, err := group.Hash(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		h2, err := group.Hash(ctx, decoded)
		if err != nil || h1 != h2 {
			t.Fatal(h1, h2, err)
		}
	}
	if _, err := XIDValue(oid, 1); err == nil {
		t.Fatal("OID accepted as XID descriptor")
	}
	if _, err := OIDValue(xid, 1); err == nil {
		t.Fatal("XID accepted as OID descriptor")
	}
	if _, err := BindComparison(xid, xid); err == nil {
		t.Fatal("narrow XID enabled ordering provider")
	}
	if _, err := PlanCoercion(xid.Usage(), oid.Usage(), Explicit); err == nil {
		t.Fatal("narrow XID enabled OID casts")
	}
}
