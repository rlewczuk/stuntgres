package typedef

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// TestWithoutDomainCheck pins the pure domain CHECK removal: the input stays
// unchanged, only the named check goes, and the SQL type's own-check flag
// follows the remaining checks while the revision is kept.
func TestWithoutDomainCheck(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			integer = d
		}
	}
	domainType, err := sqltype.Domain(47001, "d", "public.d", integer.Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Type{SQLType: domainType, ObjectRevision: 9, DomainChecks: []catalog.DomainCheckConstraint{{OID: 1, Name: "a"}, {OID: 2, Name: "b"}}}
	one, err := WithoutDomainCheck(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.DomainChecks) != 2 || !d.SQLType.HasDomainChecks() {
		t.Fatal("input domain changed")
	}
	if len(one.DomainChecks) != 1 || one.DomainChecks[0].OID != 2 || !one.SQLType.HasDomainChecks() || one.ObjectRevision != 9 {
		t.Fatalf("after removing 1: %+v", one.DomainChecks)
	}
	none, err := WithoutDomainCheck(one, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.DomainChecks) != 0 || none.SQLType.HasDomainChecks() || none.SQLType.ID() != domainType.ID() || none.Name() != "d" {
		t.Fatalf("after removing 2: %+v", none.DomainChecks)
	}
}
