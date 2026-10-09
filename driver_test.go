package stuntgres_test

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres"
)

func TestErrorFieldsPreservation(t *testing.T) {
	base := &stuntgres.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
		Message: "unrelated English primary", Detail: "detail\nline", Hint: "hint\nline",
		Position: "8", InternalPosition: "3", InternalQuery: "SELECT 1 +",
		Context: "context\nline", DataType: "only_type", Constraint: "only_constraint",
	}
	for depth := 0; depth <= 2; depth++ {
		var err error = base
		for range depth {
			err = fmt.Errorf("wrapped: %w", err)
		}
		var got *stuntgres.Error
		var state interface{ SQLState() string }
		if !errors.As(err, &got) || !errors.As(err, &state) || state.SQLState() != "42501" ||
			got.Severity != "ERROR" || got.Detail != base.Detail || got.Hint != base.Hint ||
			got.Context != base.Context || got.Position != "8" || got.InternalPosition != "3" ||
			got.InternalQuery != "SELECT 1 +" || got.DataType != "only_type" ||
			got.Constraint != "only_constraint" {
			t.Fatalf("wrapped fields at depth %d: %#v (%v)", depth, got, err)
		}
	}
	base.Message = "different message"
	if base.SQLState() != "42501" || base.Severity != "ERROR" || base.Error() != "stuntgres: different message" {
		t.Fatalf("message affected structured fields: %#v", base)
	}
	for _, tc := range []struct {
		name string
		err  stuntgres.Error
	}{
		{"schema", stuntgres.Error{Schema: "only_schema"}},
		{"table", stuntgres.Error{Table: "only_table"}},
		{"column", stuntgres.Error{Column: "only_column"}},
		{"datatype", stuntgres.Error{DataType: "only_type"}},
		{"constraint", stuntgres.Error{Constraint: "only_constraint"}},
		{"internal_query", stuntgres.Error{InternalQuery: "SELECT 1 +"}},
		{"cancellation", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error = &tc.err
			err = fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", err))
			var got *stuntgres.Error
			if !errors.As(err, &got) || got == nil || *got != tc.err {
				t.Fatalf("error fields or omitted siblings changed: %#v", got)
			}
		})
	}
}
