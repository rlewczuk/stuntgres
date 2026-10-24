package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONPathDatetimeGrammar(t *testing.T) {
	d := testTypes()[JSONPath]
	ctx := context.Background()
	for _, path := range []string{`$.datetime()`, `$.datetime("DD.MM.YYYY")`, `$.date()`, `$.time(0)`, `$.time_tz(6)`, `$.timestamp(10)`, `$.timestamp_tz(2147483648)`} {
		v, err := d.Input(ctx, path, -1)
		if err != nil {
			t.Fatal(path, err)
		}
		text, err := d.Output(ctx, v)
		if err != nil || text != path {
			t.Fatal(path, text, err)
		}
		copy, err := d.Input(ctx, text, -1)
		if err != nil || !SameDatum(v, copy) {
			t.Fatal("roundtrip", path, err)
		}
	}
	for _, tc := range []struct{ path, message string }{
		{`$.date(1)`, `syntax error at or near "1" of jsonpath input`},
		{`$.datetime(1)`, `syntax error at or near "1" of jsonpath input`},
		{`$.time(-1)`, `syntax error at or near "-" of jsonpath input`},
		{`$.time(1.5)`, `syntax error at or near "1.5" of jsonpath input`},
		{`$.timestamp(null)`, `syntax error at end of jsonpath input`},
		{`$."date"()`, `syntax error at or near "(" of jsonpath input`},
	} {
		_, err := d.Input(ctx, tc.path, -1)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s diagnostic=%+v want=%+v", tc.path, got, want)
		}
	}
}
