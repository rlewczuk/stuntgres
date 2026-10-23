package sqltype

import "testing"

func TestRangeTypeClassIdentityAccess(t *testing.T) {
	d := testTypes()[Regclass]
	// Binary/imported identity payloads need no decimal text to be authoritative.
	v := Value{typ: d, oid: 4000000000, integer: 1}
	var id uint32
	var err error
	if allocations := testing.AllocsPerRun(100, func() { id, err = v.ClassOID() }); allocations != 0 || err != nil || id != 4000000000 {
		t.Fatal("physical identity accessor formatted or changed datum", id, allocations, err)
	}
	for _, bad := range []Value{{}, Null(d), String(d, "unresolved_relation"), {typ: testTypes()[Int4], integer: 1}} {
		if _, err := bad.ClassOID(); err == nil {
			t.Fatal("unmaterialized or foreign alias accepted")
		}
	}
	domain, err := Domain(940010, "relation_alias", "public.relation_alias", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(v)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := wrapped.ClassOID(); err != nil || id != 4000000000 {
		t.Fatal("domain alias identity", id, err)
	}
}
