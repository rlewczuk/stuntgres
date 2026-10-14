package functions

import (
	"context"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinaryConversionRuntimeValues(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ids           []sqltype.ID
		result        sqltype.ID
		raw           []string
		want, handler string
	}{
		{"convert", []sqltype.ID{sqltype.Bytea, sqltype.Name, sqltype.Name}, sqltype.Bytea, []string{"\xe9", "LATIN1", "UTF8"}, "é", "iso8859_1_to_utf8"},
		{"convert_from", []sqltype.ID{sqltype.Bytea, sqltype.Name}, sqltype.Text, []string{"\xe9", "LATIN1"}, "é", "iso8859_1_to_utf8"},
		{"convert_to", []sqltype.ID{sqltype.Text, sqltype.Name}, sqltype.Bytea, []string{"é", "LATIN1"}, "\xe9", "utf8_to_iso8859_1"},
		{"encode", []sqltype.ID{sqltype.Bytea, sqltype.Text}, sqltype.Text, []string{"\xe9", "hex"}, "e9", ""},
		{"decode", []sqltype.ID{sqltype.Text, sqltype.Text}, sqltype.Bytea, []string{"e9", "hex"}, "\xe9", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []int
			node := conversionUnitCall(t, tc.name, tc.ids, tc.result, tc.raw, &order)
			ctx := &conversionUnitContext{Context: context.Background(), denyLookup: tc.handler == ""}
			if tc.handler != "" {
				handler, ok := sqltype.RegisteredEncodingConversion(tc.handler)
				if !ok {
					t.Fatal("real registered conversion missing", tc.handler)
				}
				ctx.handler = &handler
			}
			value, err := node.Eval(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var actual string
			if tc.result == sqltype.Bytea {
				actual, err = value.Binary()
			} else {
				actual, err = value.Text()
			}
			if err != nil || actual != tc.want {
				t.Fatal(actual, tc.want, err)
			}
			wantOrder := []int{0, 1}
			if len(tc.ids) == 3 {
				wantOrder = append(wantOrder, 2)
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatal(order)
			}
		})
	}
}
