package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestInternalCharInputOutput(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Char]
	for _, tc := range []struct{ input, output string }{
		{"r", "r"}, {"abc", "a"}, {" ab", " "}, {"", ""},
		{"é", `\303`}, {"界", `\347`}, {"🙂", `\360`},
		{`\101`, "A"}, {`\000`, ""}, {`\177`, "\x7f"},
		{`\200`, `\200`}, {`\377`, `\377`}, {`\400`, ""}, {`\777`, `\377`},
		{`\12`, `\`}, {`\378`, `\`}, {`\377x`, `\`}, {"'", "'"}, {`\`, `\`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			v, err := d.Input(ctx, tc.input, -1)
			if err != nil || v.Type() != d || v.IsNull() {
				t.Fatal(v, err)
			}
			got, err := d.Output(ctx, v)
			if err != nil || got != tc.output {
				t.Fatalf("output=%q err=%v want %q", got, err, tc.output)
			}
		})
	}
	for _, tc := range []struct{ input, byteText string }{
		{"\xff", "0xff"}, {"a\xff", "0xff"}, {"\x00", "0x00"}, {"a\x00b", "0x00"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.byteText}
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) || v.Type() != nil {
			t.Fatalf("input %q: value=%v diagnostic=%#v want %#v", tc.input, v, err, want)
		}
	}
	zero, err := d.Input(ctx, "", -1)
	if err != nil || zero.IsNull() || !Null(d).IsNull() || Null(d).Type() != d {
		t.Fatal("zero versus typed NULL", zero, err)
	}
}
