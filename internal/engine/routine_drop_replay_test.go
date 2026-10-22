package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// These are private producer-free binder outcomes, not a second SQL adapter
// scenario. The reference persistence definition owns actual Restore behavior.
func TestRoutineDropReplayClassification(t *testing.T) {
	e := New()
	run(t, e, "CREATE FUNCTION rd_unit_amb(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$")
	run(t, e, "CREATE FUNCTION rd_unit_amb(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$")
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	integer := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}).SQLType.Usage()
	input, _ := op.Session.Settings.Presentation()
	for _, tc := range []struct {
		source, code string
		missing      bool
	}{
		{"SELECT rd_unit_missing(1)", "42883", true},
		{"SELECT public.num_nulls()", "42883", true},
		{"SELECT public.num_nonnulls()", "42883", true},
		{"SELECT public.concat()", "42883", true},
		{"SELECT public.regexp_count()", "42883", true},
		{"SELECT public.makeaclitem()", "42883", true},
		{"SELECT public.pg_sleep()", "42883", true},
		{"SELECT public.nextval()", "42883", true},
		{"SELECT public.lower(1)", "42883", true},
		{"SELECT public.sum(1)", "42883", true},
		{"SELECT public.bit_and(1)", "42883", true},
		{"SELECT public.avg(1)", "42883", true},
		{"SELECT public.bool_and(true)", "42883", true},
		{"SELECT public.array_agg(1)", "42883", true},
		{"SELECT public.string_agg('a','b')", "42883", true},
		{"SELECT public.corr(1,2)", "42883", true},
		{"SELECT public.row_number() OVER ()", "42883", true},
		{"SELECT f1(ROW(1))", "", false},
		{"SELECT f1(ROW(1)), missing_column", "42703", false},
		{"SELECT 1 OPERATOR(public.###) 2", "42883", false},
		{"SELECT rd_unit_amb(NULL)", "42725", false},
		{"SELECT x FROM rd_unit_missing_table", "42P01", false},
		{"SELECT 1::rd_unit_missing_type", "42704", false},
		{"SELECT 'x'::text", "42P13", false},
		{"SELECT 1", "", false},
	} {
		definition, err := proc.New(tc.source, parser.Position{}, nil, integer)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		d := &catalog.Procedure{ObjectName: "rd_unit_replay", SQL: definition, Result: 23}
		before := auth.Catalog.State()
		binding, _, err := e.bindSQLRoutineLocked(op, auth, d, input, nil)
		var diagnostic *sqlstate.Error
		if tc.code == "" {
			if err != nil {
				t.Fatal(tc.source, err)
			}
		} else if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc.source, err)
		}
		if binding.UnresolvedCallable != tc.missing {
			t.Fatalf("%s: classification=%v error=%v", tc.source, binding.UnresolvedCallable, err)
		}
		if auth.Catalog.State() != before {
			t.Fatal("validation changed catalog")
		}
	}
	for _, source := range []string{"SELECT rd_unit_missing(1), $1", "SELECT (SELECT $1), rd_unit_missing(1)", "SELECT 1 +"} {
		if _, err := proc.New(source, parser.Position{}, nil, integer); err == nil {
			t.Fatal("invalid immutable definition accepted", source)
		}
	}
}
