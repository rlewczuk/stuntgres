package types_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestBooleanDiagnosticsBinding(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE b(id INT,flag BOOL)")
	for _, populated := range []bool{false, true} {
		if populated {
			enginetest.Run(t, e, "INSERT INTO b VALUES (99,TRUE)")
		}
		for _, tc := range []struct{ query, position string }{
			{"SELECT id FROM b WHERE flag='o'", "29"},
			{"/*é*/ SELECT id FROM b WHERE flag='o'", "35"},
		} {
			for _, execute := range []bool{false, true} {
				stmt := enginetest.Statement(t, tc.query)
				var err error
				if execute {
					_, err = enginetest.Execute(e, context.Background(), stmt, nil)
				} else {
					_, err = enginetest.Describe(e, stmt)
				}
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
					Message: `invalid input syntax for type boolean: "o"`, Position: tc.position}
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("binding %q = %#v (%v), want %#v", tc.query, got, err, want)
				}
			}
		}
	}
	_, err := enginetest.Describe(e, enginetest.Statement(t, "SELECT id FROM b WHERE id='bad'"))
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
		Message: `invalid input syntax for type integer: "bad"`}
	if !errors.As(err, &got) || got.Severity != want.Severity || got.SeverityUnlocalized != want.SeverityUnlocalized || got.Code != want.Code || got.Message != want.Message {
		t.Fatalf("integer seam changed: %#v (%v)", got, err)
	}
	_, err = enginetest.Describe(e, enginetest.Statement(t, "SELECT id FROM b WHERE flag=1"))
	if !errors.As(err, &got) || got.Code != "42883" {
		t.Fatalf("operator mismatch changed: %#v (%v)", got, err)
	}
}
