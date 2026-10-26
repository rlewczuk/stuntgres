package schemacmds_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// These are private producer-free binder outcomes, not a second SQL adapter
// scenario. The reference persistence definition owns actual Restore behavior.
func TestRoutineDropReplayBindingDiagnostics(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION rd_unit_amb(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1$$")
	enginetest.Run(t, e, "CREATE FUNCTION rd_unit_amb(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 2$$")
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	integer := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}).SQLType.Usage()
	input, _ := op.Session.Settings.Presentation()
	for _, tc := range []struct {
		source, code string
	}{
		{"SELECT rd_unit_missing(1)", "42883"},
		{"SELECT public.num_nulls()", "42883"},
		{"SELECT public.num_nonnulls()", "42883"},
		{"SELECT public.concat()", "42883"},
		{"SELECT public.regexp_count()", "42883"},
		{"SELECT public.makeaclitem()", "42883"},
		{"SELECT public.pg_sleep()", "42883"},
		{"SELECT public.nextval()", "42883"},
		{"SELECT public.lower(1)", "42883"},
		{"SELECT public.sum(1)", "42883"},
		{"SELECT public.bit_and(1)", "42883"},
		{"SELECT public.avg(1)", "42883"},
		{"SELECT public.bool_and(true)", "42883"},
		{"SELECT public.array_agg(1)", "42883"},
		{"SELECT public.string_agg('a','b')", "42883"},
		{"SELECT public.corr(1,2)", "42883"},
		{"SELECT public.row_number() OVER ()", "42883"},
		{"SELECT f1(ROW(1))", ""},
		{"SELECT f1(ROW(1)), missing_column", "42703"},
		{"SELECT 1 OPERATOR(public.###) 2", "42883"},
		{"SELECT rd_unit_amb(NULL)", "42725"},
		{"SELECT x FROM rd_unit_missing_table", "42P01"},
		{"SELECT 1::rd_unit_missing_type", "42704"},
		{"SELECT 'x'::text", "42P13"},
		{"SELECT 1", ""},
	} {
		definition, err := proc.New(tc.source, parser.Position{}, nil, integer)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		d := &catalog.Procedure{ObjectName: "rd_unit_replay", SQL: definition, Result: 23}
		before := auth.Catalog.State()
		_, _, err = schemacmds.BindSQLRoutineLocked(e, op, auth, d, input, nil)
		var diagnostic *sqlstate.Error
		if tc.code == "" {
			if err != nil {
				t.Fatal(tc.source, err)
			}
		} else if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc.source, err)
		}
		if auth.Catalog.State() != before {
			t.Fatal("validation changed catalog")
		}
	}
	for _, source := range []string{"SELECT rd_unit_missing(1), $1", "SELECT rd_unit_missing(1), $2", "SELECT (SELECT $1), rd_unit_missing(1)", "SELECT 1 +"} {
		_, err := proc.New(source, parser.Position{}, nil, integer)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(strings.Index(source, "$1") + 1)}
		if strings.HasSuffix(source, "$2") {
			want.Message, want.Position = "there is no parameter $2", strconv.Itoa(strings.Index(source, "$2")+1)
		}
		if source == "SELECT 1 +" {
			want.Code, want.Message, want.Position = "42601", "syntax error at end of input", strconv.Itoa(len(source)+1)
		}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("immutable definition construction %q: got %+v (%v), want %+v", source, got, err, want)
		}
	}
}
