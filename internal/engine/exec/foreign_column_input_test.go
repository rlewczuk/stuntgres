package exec

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
)

func TestForeignColumnInputDiagnostics(t *testing.T) {
	f := &ForeignQuerySource{
		Columns: []catalog.Column{{Name: "id"}, {Name: "tail"}, {Name: "bytes"}},
		Request: catalog.ForeignRequest{Table: "original_table"},
	}
	execution := &ForeignExecution{Plan: catalog.ForeignPlan{Attributes: []int32{7, 3}}, Slots: []int{2, 0}}
	node := &plan.SourceNode{RangeName: "f", ColumnAliases: []string{"retired", "retained_id", "retained_tail", "picked"}}
	// Retained aliases include an absent layout slot. The input slot is dense 2,
	// projected ordinal 0, stable attribute 7 and retained alias slot 3.
	source := &Source{Input: &Source{}, Layout: []int{-1, 0, 1, 2}}
	cause := errors.New("original local input cause")
	diagnostic := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023",
		Message: `invalid hexadecimal digit: "G"`, Detail: "input detail", Hint: "input hint",
		Position: "4", InternalPosition: "2", InternalQuery: "input fragment", Context: "nested conversion",
		Schema: "input_schema", Table: "input_table", Column: "input_column", DataType: "input_type",
		Constraint: "input_constraint", File: "input_file", Line: "8", Routine: "input_routine",
	}
	input := sqlstate.WithDiagnostic(cause, &diagnostic)
	marked := fmt.Errorf("scan boundary: %w", &catalog.ForeignColumnInputError{Attribute: 7, Err: input})
	assertDiagnostic := func(err error) {
		t.Helper()
		var actual *sqlstate.Error
		want := diagnostic
		want.Context += "\n" + `column "picked" of foreign table "f"`
		if !errors.As(err, &actual) || !reflect.DeepEqual(*actual, want) {
			t.Fatal("projected input diagnostic", err, actual, want)
		}
		if !errors.Is(err, cause) || !errors.Is(err, input) {
			t.Fatal("input diagnostic lost its original cause", err)
		}
		var retained *catalog.ForeignColumnInputError
		if !errors.As(err, &retained) || retained.Attribute != 7 {
			t.Fatal("diagnostic discarded attributed cause ownership")
		}
	}
	assertDiagnostic(f.scanDiagnostic(marked, execution, node, source))
	if diagnostic.Context != "nested conversion" {
		t.Fatal("local context mutated the original diagnostic", diagnostic)
	}
	for _, unmarked := range []error{cause, input} {
		if got := f.scanDiagnostic(unmarked, execution, node, source); got != unmarked {
			t.Fatal("unmarked callback error was changed", got)
		}
	}
	if got := f.scanDiagnostic(nil, execution, node, source); got != nil {
		t.Fatal("EOF acquired an error", got)
	}
	for _, test := range []struct {
		name      string
		err       error
		execution *ForeignExecution
	}{
		{"unprojected", &catalog.ForeignColumnInputError{Attribute: 99, Err: input}, execution},
		{"missing_slots", marked, &ForeignExecution{Plan: execution.Plan}},
		{"invalid_slot", marked, &ForeignExecution{Plan: execution.Plan, Slots: []int{3, 0}}},
		{"missing_cause", &catalog.ForeignColumnInputError{Attribute: 7}, execution},
		{"nil_marker", (*catalog.ForeignColumnInputError)(nil), execution},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := f.scanDiagnostic(test.err, test.execution, node, source)
			var record *sqlstate.Error
			if err == nil || err.Error() != "stuntgres: invalid foreign column input attribute" || errors.As(err, &record) {
				t.Fatal("invalid input marker acquired guessed SQL context", err)
			}
		})
	}
	t.Run("async_completion", func(t *testing.T) {
		request := asyncUnitReady(nil, false, marked)
		execution.async = foreignAsyncState{
			request: request, ready: request.Ready(),
			inputError: func(err error) error { return f.scanDiagnostic(err, execution, node, source) },
		}
		assertDiagnostic(execution.settleAsync(context.Background(), false))
		if request.completes != 1 || request.closes != 1 || execution.async.request != nil {
			t.Fatal("failed async input was not settled exactly once", request)
		}
		if err := execution.settleAsync(context.Background(), false); err != nil || request.completes != 1 || request.closes != 1 {
			t.Fatal("completed input was adapted/closed again", err, request)
		}
	})
	t.Run("reserved_driver_error", func(t *testing.T) {
		err := f.scanDiagnostic(&catalog.ForeignColumnInputError{Attribute: 7, Err: driver.ErrBadConn}, execution, node, source)
		if err == nil || errors.Is(err, driver.ErrBadConn) {
			t.Fatal("attributed input bypassed callback sentinel sanitization", err)
		}
	})
}
