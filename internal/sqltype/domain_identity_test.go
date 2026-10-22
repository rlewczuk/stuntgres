package sqltype

import "testing"

func TestDomainIdentityDescriptor(t *testing.T) {
	originalBase := testTypes()[Int4]
	parent, err := Domain(49600, "parent", "public.parent", originalBase.Usage(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	child, err := Domain(49601, "child", "public.child", parent.Usage())
	if err != nil {
		t.Fatal(err)
	}
	freshBase := testTypes()[Int4]
	freshParent, err := parent.RebindDomain(freshBase.Usage(), "renamed", "moved.renamed")
	if err != nil {
		t.Fatal(err)
	}
	freshChild, err := child.RebindDomain(freshParent.Usage(), "child", "public.child")
	if err != nil {
		t.Fatal(err)
	}
	if freshParent == parent || freshParent.ID() != parent.ID() || freshParent.Name() != "renamed" || freshParent.SQLName() != "moved.renamed" || freshParent.DomainBase().Type != freshBase || !freshParent.HasDomainChecks() || !freshParent.OwnDomainNotNull() || !freshParent.DomainNotNull() {
		t.Fatal("same-ID name/base rebuild lost own authority", freshParent)
	}
	if freshChild.ID() != child.ID() || freshChild.DomainBase().Type != freshParent || !freshChild.HasDomainChecks() || !freshChild.DomainNotNull() || freshChild.OwnDomainNotNull() {
		t.Fatal("inherited authority lost", freshChild)
	}
	if parent.Name() != "parent" || parent.SQLName() != "public.parent" || parent.DomainBase().Type != originalBase || child.DomainBase().Type != parent {
		t.Fatal("old immutable generation changed")
	}
	for _, tc := range []struct {
		base              Usage
		name, declaration string
	}{{Usage{}, "renamed", "moved.renamed"}, {freshBase.Usage(), "", "moved.renamed"}, {freshBase.Usage(), "renamed", ""}} {
		if _, err := parent.RebindDomain(tc.base, tc.name, tc.declaration); err == nil {
			t.Fatal("invalid descriptor admitted", tc)
		}
	}
	builtin := testTypes()[13248] // Seeded information_schema.sql_identifier.
	named, err := builtin.RebindDomain(testTypes()[Name].Usage(), "renamed", "moved.renamed")
	if err != nil || !named.IsBuiltin() || named.IsUserDefined() || named.Name() != "renamed" || named.ID() != builtin.ID() || named.AssociatedArray() != builtin.AssociatedArray() || named.NamespaceName() != builtin.NamespaceName() || builtin.Name() != "sql_identifier" {
		t.Fatal("named rebind lost immutable inventory provenance", err)
	}
}
