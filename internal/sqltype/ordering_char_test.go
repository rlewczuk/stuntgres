package sqltype

import (
	"context"
	"testing"
)

func TestOrderingCatalogChar(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Char]
	provider, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"S", "r", -1}, {"r", "v", -1}, {"v", "S", 1}, {"v", "v", 0},
		{"", "S", -1}, {"v", `\303`, -1}, {`\303`, `\377`, -1},
	} {
		a, err := d.Input(ctx, tc.a, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := d.Input(ctx, tc.b, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := provider.Compare(ctx, a, b, DefaultInputEnvironment())
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		reverse, err := provider.Compare(ctx, b, a, DefaultInputEnvironment())
		if err != nil || reverse != -tc.want {
			t.Fatal(tc, reverse, err)
		}
	}
}
