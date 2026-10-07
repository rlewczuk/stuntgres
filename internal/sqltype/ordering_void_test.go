package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestOrderingVoidRegression(t *testing.T) {
	d := testTypes()[Void]
	if !d.Concrete() {
		t.Fatal("VOID must remain a finalized runtime result")
	}
	value := VoidValue(d)
	text, err := d.Output(context.Background(), value)
	if err != nil || text != "" || value.IsNull() {
		t.Fatal(text, value, err)
	}
	_, err = BindOrdering(d.Usage())
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query."}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("got %#v (%v), want %#v", diagnostic, err, want)
	}
	for _, invalid := range []Usage{{}, testTypes()[Unknown].Usage(), {Type: d, Typmod: -1, Collation: CollationRef{ID: 1}}} {
		_, err := BindOrdering(invalid)
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("invalid usage must remain an invariant, not a SQL capability error", invalid, err)
		}
	}
}
