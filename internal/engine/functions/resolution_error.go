package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// SignatureHint is the hint PostgreSQL attaches to missing-function errors.
const SignatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."

// ResolutionError builds a missing-function or ambiguous-function diagnostic
// without a composite-recovery note. Callers requiring that note use
// NotedResolutionError or Binder.SignatureError.
func ResolutionError(name parser.ObjectName, pos parser.Position, names []string, ambiguous bool) error {
	code, ending, hint := "42883", "does not exist", SignatureHint
	if ambiguous {
		code, ending, hint = "42725", "is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."
	}
	d := expr.ErrorAt(code, fmt.Sprintf("function %s(%s) %s", qualifiedName(name), strings.Join(names, ", "), ending), pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

// ArrayTypeName spells signature types: arrays display the visible leaf name,
// not the internal array descriptor's frozen qualified declaration. Scalar
// naming remains catalog-owned.
func ArrayTypeName(b Binder, d *sqltype.Descriptor) string {
	if d != nil && d.IsArray() {
		return b.DiagnosticTypeName(d.Element()) + "[]"
	}
	return b.DiagnosticTypeName(d)
}

// DirectResolutionError reports a failed overload choice over arguments from
// AnalyzeDirectArguments: unknown literals display as unknown, a missing
// signature goes through Binder.SignatureError and an ambiguous one is 42725.
func DirectResolutionError(b Binder, raw parser.FunctionExpr, args []expr.Expr, unknown []bool, ambiguous bool) error {
	types := make([]string, len(args))
	for i, arg := range args {
		types[i] = "unknown"
		if !unknown[i] && arg.ResultUsage().Type != nil {
			types[i] = arg.ResultUsage().Type.DiagnosticName()
		}
	}
	if ambiguous {
		return ResolutionError(raw.Name, raw.Pos, types, true)
	}
	return b.SignatureError(raw.Name, raw.Pos, types)
}

// NotedResolutionError is ResolutionError for bind code whose missing
// signature must reach the engine: 42883 goes through Binder.SignatureError,
// while an ambiguous call is reported without a note.
func NotedResolutionError(b Binder, name parser.ObjectName, pos parser.Position, names []string, ambiguous bool) error {
	if ambiguous {
		return ResolutionError(name, pos, names, true)
	}
	return b.SignatureError(name, pos, names)
}

// DiagnosticNameOrUnknown spells an analyzed argument type, or "unknown" for
// a deferred literal. Unlike Binder.DiagnosticTypeName it never qualifies.
func DiagnosticNameOrUnknown(d *sqltype.Descriptor) string {
	if d != nil {
		return d.DiagnosticName()
	}
	return "unknown"
}
