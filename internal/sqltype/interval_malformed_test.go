package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntervalMalformedYearMonthDiagnostic(t *testing.T) {
	d := testTypes()[Interval]
	for _, input := range []string{"1-foo", "1:abc"} {
		_, err := d.Input(context.Background(), input, -1)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type interval: \"" + input + "\""}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(input, err, want)
		}
	}
}
