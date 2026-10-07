package sqltype

import (
	"context"
	"testing"
)

func TestOrderingRegclassRegression(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Regclass]
	provider, err := BindComparison(d, d)
	if err != nil {
		t.Fatal(err)
	}
	// The sequence binder's canonical regclass representation is unsigned OID
	// text. These real type values distinguish numeric order from lexical order,
	// signed int32 order, and the former silent-equality fallthrough.
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1", "2", -1},
		{"2", "10", -1},
		{"2147483647", "2147483648", -1},
		{"2147483648", "4294967295", -1},
		{"4294967295", "1", 1},
		{"4294967295", "4294967295", 0},
		{"0", "1", -1},
	} {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			got, err := provider.Compare(ctx, String(d, tc.a), String(d, tc.b))
			if err != nil || got != tc.want {
				t.Fatalf("comparison got %d, %v; want %d", got, err, tc.want)
			}
			reverse, err := provider.Compare(ctx, String(d, tc.b), String(d, tc.a))
			if err != nil || reverse != -tc.want {
				t.Fatalf("reverse got %d, %v; want %d", reverse, err, -tc.want)
			}
		})
	}
}
