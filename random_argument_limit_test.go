package stuntgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
)

func TestRandomArgumentLimit(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	if _, e := c.ExecContext(ctx, "CREATE TABLE limits(id INT)"); e != nil {
		t.Fatal(e)
	}
	arguments := make([]string, 101)
	for i := range arguments {
		arguments[i] = "1"
	}
	for _, name := range []string{"random", "random_normal", "setseed"} {
		// At exactly 100, signature lookup still wins over the general limit.
		q := "SELECT " + name + "(" + strings.Join(arguments[:100], ",") + ")"
		want := stuntgres.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883",
			Message:  "function " + name + "(" + strings.Repeat("integer, ", 99) + "integer) does not exist",
			Hint:     "No function matches the given name and argument types. You might need to add explicit type casts.",
			Position: "8",
		}
		for _, prepared := range []bool{false, true} {
			var e error
			if prepared {
				s, err := c.PrepareContext(ctx, q)
				if s != nil {
					s.Close()
				}
				e = err
			} else {
				_, e = c.ExecContext(ctx, q)
			}
			var got *stuntgres.Error
			if !errors.As(e, &got) || *got != want {
				t.Fatalf("%s 100 arguments prepared=%v got %#v (%v) want %#v", name, prepared, got, e, want)
			}
		}
		for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
			q := prefix + name + "(" + strings.Join(arguments, ",") + ")"
			pos := "8"
			if prefix != "SELECT " {
				pos = "14"
			}
			want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: pos}
			for _, prepared := range []bool{false, true} {
				var e error
				if prepared {
					s, err := c.PrepareContext(ctx, q)
					if s != nil {
						s.Close()
					}
					e = err
				} else {
					_, e = c.ExecContext(ctx, q)
				}
				var got *stuntgres.Error
				if !errors.As(e, &got) || *got != want {
					t.Fatalf("%s prepared=%v got %#v (%v) want %#v", name, prepared, got, e, want)
				}
			}
		}
	}
	arguments[0] = "missing"
	q := "SELECT random(" + strings.Join(arguments, ",") + ") FROM limits"
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing" does not exist`, Position: "15"}
	for _, prepared := range []bool{false, true} {
		var e error
		if prepared {
			s, err := c.PrepareContext(ctx, q)
			if s != nil {
				s.Close()
			}
			e = err
		} else {
			_, e = c.ExecContext(ctx, q)
		}
		var got *stuntgres.Error
		if !errors.As(e, &got) || *got != want {
			t.Fatalf("child-analysis precedence: got %#v (%v) want %#v", got, e, want)
		}
	}
}
