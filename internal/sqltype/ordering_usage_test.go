package sqltype

import (
	"context"
	"testing"
)

func TestOrderingUsage(t *testing.T) {
	for _, d := range Builtins() {
		t.Run(d.Name(), func(t *testing.T) {
			provider, err := BindOrdering(d.Usage())
			if !d.Concrete() {
				if err == nil {
					t.Fatal("lookup-only identity admitted to ordering")
				}
				return
			}
			// Concrete means a valid runtime result, not a sortable SQL type.
			if d.ID() == Void || d.IsArray() || d.ID() == PgNodeTree || d.ID() == JSON || d.ID() == JSONPath || isBitString(d.ID()) || d.ID() == Xid || d.ID() == Int4Range || d.ID() == Int8Range {
				if err == nil {
					t.Fatalf("%s ordering admitted", d.Name())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// SQL null placement is the sort stage's policy, never this strict
			// nonnull provider, even when the builtin capability binds successfully.
			if _, err := provider.Compare(context.Background(), Null(d), Null(d), InputEnvironment{}); err == nil {
				t.Fatal("nonnull provider admitted NULL")
			}
			invalid := d.Usage()
			invalid.Collation.ID = 999999
			if _, err := BindOrdering(invalid); err == nil {
				t.Fatal("unbounded collation admitted")
			}
		})
	}
	if _, err := BindOrdering(Usage{}); err == nil {
		t.Fatal("nil ordering usage admitted")
	}
}
