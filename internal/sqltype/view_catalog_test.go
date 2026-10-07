package sqltype

import (
	"context"
	"testing"
)

func TestViewCatalogTypes(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		id              ID
		database, input string
	}{{ID(26), "OID", "4294967295"}, {ID(18), "CHAR", "v"}} {
		d := types[tc.id]
		if d == nil {
			t.Fatalf("missing catalog type %d", tc.id)
		}
		if d.DatabaseName() != tc.database {
			t.Fatal(d.DatabaseName())
		}
		value, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.Output(ctx, value)
		if err != nil || out != tc.input {
			t.Fatal(out, err)
		}
		if !Null(d).IsNull() {
			t.Fatal("lost NULL")
		}
		comparison, err := BindComparison(d, d)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := comparison.Compare(ctx, value, value); err != nil || got != 0 {
			t.Fatal(got, err)
		}
	}
	for _, input := range []string{"4294967296", "-2147483649"} {
		d := types[ID(26)]
		if d == nil {
			t.Fatal("missing OID")
		}
		if _, err := d.Input(ctx, input, -1); err == nil {
			t.Fatal("out-of-domain OID accepted", input)
		}
	}
}

func TestViewCatalogOIDReleaseInputs(t *testing.T) {
	d := testTypes()[ID(26)]
	if d == nil {
		t.Fatal("missing OID")
	}
	for _, tc := range []struct{ input, output string }{{"-1", "4294967295"}, {"-2147483648", "2147483648"}, {"4294967295", "4294967295"}} {
		value, err := d.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		got, err := d.Output(context.Background(), value)
		if err != nil || got != tc.output {
			t.Fatal(tc, got, err)
		}
	}
	numericProviderError(t, func() error { _, err := d.Input(context.Background(), "bad", -1); return err }(), "22P02", `invalid input syntax for type oid: "bad"`)
}

func TestViewCatalogCHARReleaseInputs(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[ID(18)]
	for _, tc := range []struct{ input, output string }{{"", ""}, {"ab", "a"}, {"é", `\303`}, {`\303`, `\303`}, {`\166`, "v"}, {`\000`, ""}} {
		value, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		output, err := d.Output(ctx, value)
		if err != nil || output != tc.output {
			t.Fatal(tc, output, err)
		}
	}
	a, err := d.Input(ctx, "é", -1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Input(ctx, "v", -1)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := BindComparison(d, d)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := comparison.Compare(ctx, a, b); err != nil || result <= 0 {
		t.Fatal(result, err)
	}
}
