package proc

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLCursorCurrentDiagnosticPhase(t *testing.T) {
	for _, tc := range []struct {
		name             string
		simple, planning bool
	}{
		{"outer_spi_inner_simple", false, false},
		{"outer_planning_inner_simple", false, true},
		{"outer_simple_inner_spi", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &sqlstate.Error{Code: "23505", Message: "inner failure", Context: "inner trace", InternalQuery: "inner query", InternalPosition: "3"}
			before := *original
			inner := &PLExpressionExecutionError{Err: original, Simple: !tc.simple}
			var failure error = &PLExpressionExecutionError{Err: inner, Simple: tc.simple}
			if tc.planning {
				failure = &PLExpressionPlanningError{Err: inner}
			}
			query := PLCursorExpressionDiagnostic(failure, "outer", parser.Position{Character: 1}, parser.Position{Offset: 5, Character: 6})
			gotError := PLFormatError(failure, query, "outer action")
			var got *sqlstate.Error
			want := before
			if !tc.simple {
				want.Context += "\nPL/pgSQL expression \"outer\""
			}
			want.Context += "\nouter action"
			if !errors.As(gotError, &got) || *got != want || *original != before || !errors.Is(gotError, original) {
				t.Fatal("current phase lost identity, inner fields or parent callback", gotError)
			}
		})
	}
	original := &sqlstate.Error{Code: "23505", Position: "12", Context: "inner trace", InternalQuery: "inner query", InternalPosition: "3"}
	before := *original
	mapped := PLFormatError(original, PLDiagnosticQuery{Text: "parent query", Offset: 4, Callback: PLSQLCallback}, "outer action")
	var got *sqlstate.Error
	want := before
	want.Position, want.InternalPosition, want.InternalQuery, want.Context = "", "8", "parent query", "inner trace\nouter action"
	if !errors.As(mapped, &got) || *got != want || *original != before || !errors.Is(mapped, original) {
		t.Fatal("original position callback branch lost fields or appended an extra query frame", mapped)
	}
	canceled := &PLExpressionExecutionError{Err: context.Canceled, Simple: false}
	if PLFormatError(canceled, PLDiagnosticQuery{Text: "parent", Callback: PLSQLCallback}, "outer action") != canceled || !errors.Is(canceled, context.Canceled) {
		t.Fatal("non-diagnostic cancellation identity changed")
	}
}

func TestPLCaseExpressionError(t *testing.T) {
	t.Run("diagnostic_coordinates", func(t *testing.T) {
		source := "/*é*/ BEGIN CASE 1 WHEN absent(1+missing()) THEN RETURN 7; END CASE; END"
		operator := PLSearchOperatorPosition(source, 0)
		if operator.Character <= utf8.RuneCountInString(source)+1 || operator.Offset <= len(source) {
			t.Fatal("synthetic coordinate aliases body EOF")
		}
		start := parser.Position{Offset: 25, Character: 24}
		branch := PLBranch{DiagnosticQuery: `"__Case__Variable_1__" IN (absent(1+missing()))`, OperatorPosition: operator, DiagnosticOperator: 24, DiagnosticItems: 28}
		for _, tc := range []struct {
			name           string
			position, want int
		}{
			{"operator", operator.Character, branch.DiagnosticOperator},
			{"first_item", start.Character, branch.DiagnosticItems},
			{"nested_item", start.Character + 9, branch.DiagnosticItems + 9},
		} {
			t.Run(tc.name, func(t *testing.T) {
				original := &sqlstate.Error{Code: "42883", Position: strconv.Itoa(tc.position)}
				err := PLCaseExpressionError(original, branch, start)
				var mapped *sqlstate.Error
				if !errors.As(err, &mapped) {
					t.Fatal("mapped diagnostic unavailable", err)
				}
				if mapped.Position != "" || mapped.InternalPosition != strconv.Itoa(tc.want) || mapped.InternalQuery != branch.DiagnosticQuery || mapped.Context != "" || original.Position != strconv.Itoa(tc.position) {
					t.Fatal("synthetic/original coordinate confusion", mapped)
				}
			})
		}
		ordinary := context.Canceled
		if PLCaseExpressionError(ordinary, branch, start) != ordinary {
			t.Fatal("ordinary error decorated")
		}
	})
	t.Run("diagnostic_error_chain", func(t *testing.T) {
		start := parser.Position{Offset: 20, Character: 21}
		branch := PLBranch{DiagnosticQuery: `"__Case__Variable_1__" IN (called())`, DiagnosticOperator: 24, DiagnosticItems: 28}
		original := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "producer failure", Position: "21", Detail: "original detail"}
		before := *original
		failure := errors.New("provider cleanup failure")
		fatal := &PLExceptionCleanupError{Primary: original, Cleanup: failure}
		mapped := PLCaseExpressionError(fatal, branch, start)
		var cleanup *PLExceptionCleanupError
		if !errors.As(mapped, &cleanup) || cleanup != fatal || !errors.Is(mapped, original) || !errors.Is(mapped, failure) {
			t.Fatal("CASE diagnostic discarded fatal cleanup classification or originating evidence", mapped)
		}
		var diagnostic *sqlstate.Error
		want := before
		want.Position, want.InternalPosition, want.InternalQuery = "", "28", branch.DiagnosticQuery
		if !errors.As(mapped, &diagnostic) || *diagnostic != want || *original != before {
			t.Fatal("CASE decoration lost mapped diagnostic or mutated its origin", diagnostic)
		}
	})
	t.Run("diagnostic_phase", func(t *testing.T) {
		branch := PLBranch{DiagnosticQuery: `"__Case__Variable_2__" IN (12/x)`}
		for _, tc := range []struct {
			name     string
			planning bool
			context  string
		}{
			{"execution", false, ""},
			{"planning", true, `PL/pgSQL expression "` + branch.DiagnosticQuery + `"`},
			{"producer_context", false, "producer context"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				original := &sqlstate.Error{Code: "22012", Message: "division by zero"}
				if tc.name == "producer_context" {
					original.Context = tc.context
				}
				var cause error = &PLExpressionExecutionError{Err: original, Simple: true}
				if tc.planning {
					cause = &PLExpressionPlanningError{Err: original}
				}
				mapped := PLCaseExpressionError(cause, branch, parser.Position{})
				var diagnostic *sqlstate.Error
				var planning *PLExpressionPlanningError
				if !errors.As(mapped, &diagnostic) || diagnostic.Context != tc.context {
					t.Fatal("CASE diagnostic ignored producer phase/context", diagnostic)
				}
				if errors.As(mapped, &planning) != tc.planning || !errors.Is(mapped, original) {
					t.Fatal("CASE diagnostic discarded producer phase or identity", mapped)
				}
			})
		}
	})
}

func TestPLFormatErrorPositionMapping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		position string
		context  string
		query    PLDiagnosticQuery
		want     sqlstate.Error
	}{
		{"offset", "10", "", PLDiagnosticQuery{Text: "x+y", Offset: 4, Callback: PLExpressionCallbackKind},
			sqlstate.Error{InternalPosition: "6", InternalQuery: "x+y", Context: "action"}},
		{"span", "22", "", PLDiagnosticQuery{Text: "a, b", Offset: 4, Positions: []PLDiagnosticPosition{{Start: 20, End: 25, Output: 3}}, Callback: PLExpressionCallbackKind},
			sqlstate.Error{InternalPosition: "5", InternalQuery: "a, b", Context: "action"}},
		{"span_end_exclusive", "25", "", PLDiagnosticQuery{Text: "a, b", Offset: 4, Positions: []PLDiagnosticPosition{{Start: 20, End: 25, Output: 3}}, Callback: PLExpressionCallbackKind},
			sqlstate.Error{InternalPosition: "21", InternalQuery: "a, b", Context: "action"}},
		{"binding_positioned_and_framed", "7", "inner", PLDiagnosticQuery{Text: "a := b", Offset: 2, Callback: PLAssignmentBinding},
			sqlstate.Error{InternalPosition: "5", InternalQuery: "a := b", Context: "inner\nPL/pgSQL assignment \"a := b\"\naction"}},
		{"assignment_context_never_positioned", "7", "", PLDiagnosticQuery{Text: "q", Offset: 2, Callback: PLAssignmentContext},
			sqlstate.Error{Position: "7", Context: "PL/pgSQL assignment \"q\"\naction"}},
		{"query_detail_unquoted", "7", "inner", PLDiagnosticQuery{Text: "SELECT 1", Callback: PLQueryDetail},
			sqlstate.Error{Position: "7", Context: "inner\nquery: SELECT 1\naction"}},
		{"sql_unpositioned", "", "", PLDiagnosticQuery{Text: "q", Callback: PLSQLCallback},
			sqlstate.Error{Context: "SQL statement \"q\"\naction"}},
		{"no_callback", "7", "inner", PLDiagnosticQuery{Text: "q", Offset: 2},
			sqlstate.Error{Position: "7", Context: "inner\naction"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &sqlstate.Error{Code: "22012", Message: "m", Position: tc.position, Context: tc.context}
			before := *original
			var got *sqlstate.Error
			err := PLFormatError(original, tc.query, "action")
			tc.want.Code, tc.want.Message = "22012", "m"
			if !errors.As(err, &got) || *got != tc.want || *original != before || !errors.Is(err, original) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
	if canceled := PLFormatError(context.Canceled, PLDiagnosticQuery{Text: "q", Callback: PLSQLCallback}, "action"); canceled != context.Canceled {
		t.Fatal("non-diagnostic error decorated", canceled)
	}
}

type (
	testAssignmentDomain     struct{ error }
	testAssignmentPlanning   struct{ error }
	testAssignmentProjection struct{ error }
)

func (e *testAssignmentDomain) Unwrap() error             { return e.error }
func (*testAssignmentDomain) PLAssignmentDomain()         {}
func (e *testAssignmentPlanning) Unwrap() error           { return e.error }
func (*testAssignmentPlanning) PLAssignmentPlanning()     {}
func (e *testAssignmentProjection) Unwrap() error         { return e.error }
func (*testAssignmentProjection) PLAssignmentProjection() {}

func TestPLAssignmentErrorPhaseMarkers(t *testing.T) {
	d := &Definition{Source: "BEGIN\n x := 1/0; END"}
	statement := &parser.PLStatement{Pos: parser.Position{Offset: 7, Character: 8}, ExpressionEnd: parser.Position{Offset: 15, Character: 16}}
	action := "PL/pgSQL function f() line 2 at assignment"
	for _, tc := range []struct {
		name string
		wrap func(error) error
		want sqlstate.Error
	}{
		{"unmarked", func(err error) error { return err },
			sqlstate.Error{Position: "12", Context: action}},
		{"planning", func(err error) error { return &testAssignmentPlanning{err} },
			sqlstate.Error{InternalPosition: "5", InternalQuery: "x := 1/0", Context: action}},
		{"planning_domain", func(err error) error { return &testAssignmentPlanning{&testAssignmentDomain{err}} },
			sqlstate.Error{Position: "12", Context: action}},
		{"projection", func(err error) error { return &testAssignmentProjection{err} },
			sqlstate.Error{Position: "12", Context: "PL/pgSQL assignment \"x := 1/0\"\n" + action}},
		{"planning_domain_projection", func(err error) error {
			return &testAssignmentPlanning{&testAssignmentDomain{&testAssignmentProjection{err}}}
		}, sqlstate.Error{InternalPosition: "5", InternalQuery: "x := 1/0", Context: action}},
		{"cardinality", func(err error) error { return &PLCardinalityError{Err: &testAssignmentPlanning{err}} },
			sqlstate.Error{Position: "12", Context: "query: x := 1/0\n" + action}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &sqlstate.Error{Code: "22012", Message: "m", Position: "12"}
			err := PLAssignmentError(tc.wrap(original), d, func() string { return "f()" }, statement)
			var got *sqlstate.Error
			tc.want.Code, tc.want.Message = "22012", "m"
			if !errors.As(err, &got) || *got != tc.want || !errors.Is(err, original) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
	err := PLAssignmentError(context.Canceled, d, func() string { t.Fatal("identity formatted for a non-diagnostic error"); return "" }, statement)
	if err != context.Canceled {
		t.Fatal("non-diagnostic error decorated", err)
	}
}
