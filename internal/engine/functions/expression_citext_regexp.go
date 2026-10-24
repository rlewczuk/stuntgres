package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CitextRegexpCapture carries installed identity, never cached permission.
// Info.RoutineReference remains the opaque original declaration until its owner
// explicitly maps it. Name is descriptive; analyzer/stored/exec own authority.
type CitextRegexpCapture struct {
	Ref       ids.ObjectRef
	Namespace string
	Name      string
	Member    uint8
	Text      sqltype.Usage
	CanInline bool // Exact selected declaration is an eligible no-flags scalar.
	Inline    bool // Shared simplification retained the nonconstant SQL body.
}

func NewCitextRegexpCall(info expr.Info, name string, args []expr.Expr, written [7]uint8, labels [7]string, capture CitextRegexpCapture) (RegexpCall, error) {
	call := RegexpCall{Info: info, Args: args, WrittenSlots: written, Labels: labels, Citext: &capture}
	var usages [4]sqltype.Usage
	if len(args) > len(usages) {
		return call, fmt.Errorf("stuntgres: invalid citext regexp arity")
	}
	for i, argument := range args {
		if argument == nil {
			return call, fmt.Errorf("stuntgres: missing citext regexp argument")
		}
		usages[i] = argument.ResultUsage()
	}
	// SQL-standard bodies retain their declaration's bound input collation,
	// including when simplification substitutes arguments into an inline body.
	plan, err := sqltype.BindCitextRegexpFunction(name, usages[:len(args)], info.Usage, capture.Text, capture.Text.Collation, false)
	call.Plan = plan
	if err != nil {
		return call, err
	}
	return call, call.validateCitext()
}

// Structural checks never inspect the opaque declaration or confer authority.
// Its exact metadata, current provider/member and generations are checked by
// analyzer/stored owners, and current EXECUTE admission remains in exec.
func (call RegexpCall) validateCitext() error {
	if call.Citext == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("stuntgres: invalid captured citext regexp") }
	capture := call.Citext
	if !call.Usage.Valid() || len(call.Args) < 2 || len(call.Args) > 4 || call.RoutineReference == nil ||
		capture.Name == "" || capture.Namespace == "" || capture.Ref.Address.ClassID != ids.ProcedureClassID || capture.Ref.Address.ObjectID == 0 || capture.Ref.Address.SubID != 0 {
		return invalid()
	}
	if call.Plan.ArgCount() != len(call.Args) || call.Plan.ResultUsage() != call.Usage ||
		!capture.Text.Valid() || capture.Text.Type.ID() != sqltype.Text {
		return invalid()
	}
	if call.Plan.Collation() != capture.Text.Collation || call.Plan.Conflict() ||
		capture.CanInline && call.Plan.ReturnsSet() || capture.Inline && !capture.CanInline {
		return invalid()
	}
	for i, argument := range call.Args {
		if argument == nil || !argument.ResultUsage().Valid() || call.Plan.ArgumentUsage(i) != argument.ResultUsage() {
			return invalid()
		}
	}
	if !call.Args[0].ResultUsage().Type.IsCitext() || call.Args[1].ResultUsage().Type != call.Args[0].ResultUsage().Type {
		return invalid()
	}
	var seen [7]bool
	for _, slot := range call.WrittenSlots[:len(call.Args)] {
		if int(slot) >= len(call.Args) || seen[slot] {
			return invalid()
		}
		seen[slot] = true
	}
	return nil
}

func (call RegexpCall) citextRendering() (expr.Rendering, error) {
	if err := call.validateCitext(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, len(call.Args))
	for i, slot := range call.WrittenSlots[:len(call.Args)] {
		argument := call.Args[slot]
		args[i] = expr.RenderArg{Expr: argument, Label: call.Labels[i], Transport: expr.TransportCast, TransportType: argument.ResultUsage()}
	}
	name := call.Citext.Name
	return expr.Rendering{
		Tree: "CitextRegexp " + call.Citext.Namespace + "." + name,
		Inputs: call.Args, Form: expr.FormCall, Name: name, Namespace: call.Citext.Namespace,
		Quote: expr.QuoteIdentifier, Args: args, Replay: expr.Replay{QuoteNamespace: true},
	}, nil
}

func (call Regexp) StrictNull() expr.Strict {
	if call.Citext != nil {
		return expr.StrictAfter
	}
	return expr.StrictNone
}

// InlineSQLBody records the shared simplifier's nonconstant scalar boundary.
// The original stored/analyzed capture remains immutable.
func (call Regexp) InlineSQLBody() (Regexp, bool) {
	if call.Citext == nil || !call.Citext.CanInline || call.Citext.Inline {
		return call, false
	}
	capture := *call.Citext
	capture.Inline = true
	call.Citext = &capture
	return call, true
}

// CitextRegexpRuntime supplies descriptive current identity after ordinary
// routine admission. Names never select an executable provider.
type CitextRegexpRuntime interface {
	CitextRegexpDiagnosticName(RegexpCall) (string, error)
}

// Constant/parameter SQL invocation retains wrapper provenance; a scalar body
// retained by simplification exposes its underlying operation's diagnostic.
func (call RegexpCall) citextError(ctx expr.Context, err error) error {
	if err == nil || call.Citext == nil || call.Citext.Inline {
		return err
	}
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Context != "" || (diagnostic.Code != "2201B" && diagnostic.Code != "22023" && diagnostic.Code != "42P22") {
		return err
	}
	runtime, ok := ctx.(CitextRegexpRuntime)
	if !ok {
		return fmt.Errorf("stuntgres: missing citext regexp diagnostic runtime")
	}
	name, failure := runtime.CitextRegexpDiagnosticName(call)
	if failure != nil {
		return failure
	}
	copy := *diagnostic
	copy.Context = `SQL function "` + name + `" statement 1`
	return &copy
}
