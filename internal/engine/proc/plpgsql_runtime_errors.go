package proc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PLDiagnosticCallback names the SPI/query frame a runtime failure gains.
type PLDiagnosticCallback uint8

const (
	PLNoCallback PLDiagnosticCallback = iota
	PLExpressionCallbackKind
	PLSQLCallback
	PLAssignmentCallback
	PLAssignmentContext // Existing semantic-datum projection/conversion frame, no SPI position mapping.
	PLAssignmentBinding // Trigger's actual binder: preserve its ASSIGN frame and source mapping.
	PLQueryDetail       // Existing scalar assignment cardinality's unquoted "query: " frame.
)

type PLDiagnosticPosition struct{ Start, End, Output int }

// PLDiagnosticQuery text is diagnostic provenance, never executable syntax.
// Positions maps declaration-reordered actuals or a CASE operator into a
// synthesized query.
type PLDiagnosticQuery struct {
	Text      string
	Offset    int
	Positions []PLDiagnosticPosition
	Callback  PLDiagnosticCallback
}

func PLBodyDiagnostic(source string, start, end parser.Position, callback PLDiagnosticCallback) PLDiagnosticQuery {
	return PLDiagnosticQuery{Text: parser.PLExpressionSource(source, start, end), Offset: start.Character - 1, Callback: callback}
}

// PLRuntimeError formats a statement failure. identity names the routine and
// runs only when err carries a SQL diagnostic.
func PLRuntimeError(err error, d *Definition, identity func() string, start, end, origin parser.Position, action string, expression bool) error {
	query := PLDiagnosticQuery{}
	if expression && PLExpressionCallback(err) && (d.Invocation != DMLTriggerInvocation || PLTriggerExpressionBinding(err)) {
		query = PLBodyDiagnostic(d.Source, start, end, PLExpressionCallbackKind)
	}
	return PLRuntimeQueryError(err, d, identity, origin, action, query)
}

func PLRuntimeQueryError(err error, d *Definition, identity func() string, origin parser.Position, action string, query PLDiagnosticQuery) error {
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return err
	}
	context := PLActionContext(d.Source, identity(), origin, action)
	return PLFormatError(err, query, context)
}

// PLActionContext formats the shared action frame for errors and buffered
// notices. Its caller owns identity visibility and the execution boundary.
func PLActionContext(source, identity string, origin parser.Position, action string) string {
	line := strings.Count(source[:origin.Offset], "\n") + 1
	return fmt.Sprintf("PL/pgSQL function %s line %d %s", identity, line, action)
}

// PLFormatError is the single SPI/query and PL action formatter. Eligibility is
// the actual producer phase and ORIGINAL positive Position, never SQLSTATE, a
// previously mapped InternalPosition, existing Context, or source-text guesses.
func PLFormatError(err error, query PLDiagnosticQuery, action string) error {
	var original *sqlstate.Error
	if !errors.As(err, &original) {
		return err
	}
	next := *original
	if query.Callback != PLNoCallback {
		position, _ := strconv.Atoi(original.Position)
		positioned := position > 0 && query.Callback != PLAssignmentContext && query.Callback != PLQueryDetail
		if positioned {
			mapped := position - query.Offset
			for _, span := range query.Positions {
				if position >= span.Start && position < span.End {
					mapped = position - span.Start + span.Output
					break
				}
			}
			next.InternalPosition, next.InternalQuery, next.Position = strconv.Itoa(mapped), query.Text, ""
		}
		if !positioned || query.Callback == PLAssignmentBinding {
			prefix := "PL/pgSQL expression "
			switch query.Callback {
			case PLSQLCallback:
				prefix = "SQL statement "
			case PLAssignmentCallback, PLAssignmentContext, PLAssignmentBinding:
				prefix = "PL/pgSQL assignment "
			case PLQueryDetail:
				prefix = "query: "
			}
			if next.Context != "" {
				next.Context += "\n"
			}
			if query.Callback == PLQueryDetail {
				next.Context += prefix + query.Text
			} else {
				next.Context += prefix + "\"" + query.Text + "\""
			}
		}
	}
	if action != "" {
		if next.Context != "" {
			next.Context += "\n"
		}
		next.Context += action
	}
	return sqlstate.WithDiagnostic(err, &next)
}

// PLExpressionBindingError preserves failed expression analysis separately
// from execution. Trigger assignment diagnostics name the complete assignment
// on a binding failure, while a successfully bound arithmetic failure names
// only the PL statement.
type PLExpressionBindingError struct{ Err error }

func (e *PLExpressionBindingError) Error() string  { return e.Err.Error() }
func (e *PLExpressionBindingError) Unwrap() error  { return e.Err }
func (*PLExpressionBindingError) plCallback() bool { return true }

type PLExpressionPlanningError struct{ Err error }

func (e *PLExpressionPlanningError) Error() string  { return e.Err.Error() }
func (e *PLExpressionPlanningError) Unwrap() error  { return e.Err }
func (*PLExpressionPlanningError) plCallback() bool { return true }

// PLDatumAccessError identifies failed lexical-datum access during binding.
// Expression and assignment owners select their distinct callback policies.
type PLDatumAccessError struct{ Err error }

func (e *PLDatumAccessError) Error() string  { return e.Err.Error() }
func (e *PLDatumAccessError) Unwrap() error  { return e.Err }
func (*PLDatumAccessError) plCallback() bool { return true }

// PLExpressionExecutionError records the CURRENT route of every real handoff,
// including complex execution. An outer SPI/planning marker therefore shadows
// an inner callee's simple route instead of inheriting it through errors.As.
type PLExpressionExecutionError struct {
	Err    error
	Simple bool
}

func (e *PLExpressionExecutionError) Error() string    { return e.Err.Error() }
func (e *PLExpressionExecutionError) Unwrap() error    { return e.Err }
func (e *PLExpressionExecutionError) plCallback() bool { return !e.Simple }

// PLCardinalityError marks a scalar assignment query that returned more than
// one row.
type PLCardinalityError struct{ Err error }

func (e *PLCardinalityError) Error() string { return e.Err.Error() }
func (e *PLCardinalityError) Unwrap() error { return e.Err }

// PLExceptionCleanupError is fatal to handler matching: it keeps the primary
// diagnostic and the cleanup failure traversable.
type PLExceptionCleanupError struct{ Primary, Cleanup error }

func (e *PLExceptionCleanupError) Error() string {
	return fmt.Sprintf("%v (exception cleanup: %v)", e.Primary, e.Cleanup)
}
func (e *PLExceptionCleanupError) Unwrap() []error { return []error{e.Primary, e.Cleanup} }

// PLExpressionCallback reports whether the current owner's phase grants an
// expression callback frame; an unmarked error does.
func PLExpressionCallback(err error) bool {
	var phase interface{ plCallback() bool }
	return !errors.As(err, &phase) || phase.plCallback()
}

// Assignment planning has a distinct query callback from ordinary expressions.
// Datum access can fail before that assignment query exists. Inspect only the
// current binding/planning owner, never an executed callee's nested datum error.
func plAssignmentExpressionCallback(err error) bool {
	var phase interface{ plCallback() bool }
	if !errors.As(err, &phase) {
		return true
	}
	var binding error
	switch current := phase.(type) {
	case *PLDatumAccessError:
		return false
	case *PLExpressionBindingError:
		binding = current.Err
	case *PLExpressionPlanningError:
		binding = current.Err
	default:
		return phase.plCallback()
	}
	if current, ok := binding.(*PLExpressionBindingError); ok {
		binding = current.Err
	}
	_, datum := binding.(*PLDatumAccessError)
	return !datum
}

// PLTriggerExpressionBinding grants the trigger traceback only at the current
// owner's binding boundary. An inner trigger's binding failure under an outer
// execution/planning callback must not look like the outer lexical binder.
func PLTriggerExpressionBinding(err error) bool {
	var phase interface{ plCallback() bool }
	if !errors.As(err, &phase) {
		return false
	}
	switch current := phase.(type) {
	case *PLExpressionBindingError:
		return true
	case *PLExpressionPlanningError:
		_, binding := current.Err.(*PLExpressionBindingError)
		return binding
	default:
		return false
	}
}

// PLCaseExpressionError maps a simple-CASE WHEN failure into its synthesized
// query. The operator has its own disjoint coordinate; original WHEN children
// retain their original byte/character coordinates. Neither is reparsed.
func PLCaseExpressionError(err error, branch PLBranch, start parser.Position) error {
	if !PLExpressionCallback(err) {
		return err
	}
	query := PLDiagnosticQuery{
		Text:     branch.DiagnosticQuery,
		Offset:   start.Character - branch.DiagnosticItems,
		Callback: PLExpressionCallbackKind,
	}
	if branch.OperatorPosition.Character > 0 {
		query.Positions = []PLDiagnosticPosition{{Start: branch.OperatorPosition.Character, End: branch.OperatorPosition.Character + 1, Output: branch.DiagnosticOperator}}
	}
	return PLFormatError(err, query, "")
}

func PLCursorExpressionDiagnostic(err error, source string, start, end parser.Position) PLDiagnosticQuery {
	if !PLExpressionCallback(err) {
		return PLDiagnosticQuery{}
	}
	return PLBodyDiagnostic(source, start, end, PLExpressionCallbackKind)
}

// The assignment phase markers are implemented by plan's error types; proc
// matches them by method so it needs no plan import.
type (
	plAssignmentDomain     interface{ PLAssignmentDomain() }
	plAssignmentPlanning   interface{ PLAssignmentPlanning() }
	plAssignmentProjection interface{ PLAssignmentProjection() }
)

// PLAssignmentError formats a target assignment failure by producer phase.
func PLAssignmentError(err error, d *Definition, identity func() string, statement *parser.PLStatement) error {
	callback := PLNoCallback
	var planning plAssignmentPlanning
	var domain plAssignmentDomain
	var projection plAssignmentProjection
	var cardinality *PLCardinalityError
	switch {
	case errors.As(err, &cardinality):
		callback = PLQueryDetail
	case errors.As(err, &planning) && !errors.As(err, &domain) || errors.As(err, &projection):
		callback = PLAssignmentContext
		if planning != nil {
			callback = PLAssignmentCallback
		}
	}
	query := PLDiagnosticQuery{}
	if callback != PLNoCallback {
		query = PLBodyDiagnostic(d.Source, statement.Pos, statement.ExpressionEnd, callback)
	}
	return PLRuntimeQueryError(err, d, identity, statement.Pos, "at assignment", query)
}
