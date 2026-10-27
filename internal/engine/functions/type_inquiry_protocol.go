package functions

import (
	"fmt"
	"reflect"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (n TypeInquiry) Children() []expr.Expr { return n.Args }
func (n TypeInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.Usage.Type != n.result || info.RoutineReference != n.routine {
		return nil, fmt.Errorf("stuntgres: changed type inquiry provider generation")
	}
	arity := 1
	if n.Kind == FormatTypeInquiry {
		arity = 2
	}
	if err := checkChildren("type inquiry", children, arity); err != nil {
		return nil, err
	}
	if n.unknown != nil && !reflect.DeepEqual(children[0], n.unknown.transport) {
		return nil, fmt.Errorf("stuntgres: changed unknown type inquiry source")
	}
	if n.unknown != nil {
		type sourceCarrier interface{ UnknownInputSource() *expr.UnknownInput }
		accepted, ok := n.unknown.transport.(sourceCarrier)
		candidate, candidateOK := children[0].(sourceCarrier)
		if !ok || !candidateOK || candidate.UnknownInputSource() == nil || accepted.UnknownInputSource() == nil || !expr.SameTransparentInputCasts(candidate.UnknownInputSource().Chain, accepted.UnknownInputSource().Chain) {
			return nil, fmt.Errorf("stuntgres: changed unknown type inquiry chain generation")
		}
	}
	n.Info, n.Args = info, children
	if n.Kind == TypeofInquiry && n.unknown == nil && n.stringIO == nil && children[0].ResultUsage().Type != nil {
		n.logical = children[0].ResultUsage().Type.ID()
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}

// Retag is the authorized descriptor-generation path. It may replace the
// concrete TEXT and authenticated cast generations, but never invents provenance.
func (n TypeInquiry) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if err := n.Validate(); err != nil {
		return nil, false, err
	}
	n.result = env.Type(n.result)
	n.routine = info.RoutineReference
	for i, formal := range n.formals {
		if formal != nil {
			n.formals[i] = env.Type(formal)
		}
	}
	if n.stringIO != nil {
		target := env.Type(n.stringIO)
		changed = changed || target != n.stringIO
		n.stringIO = target
		chain, chainChanged, err := expr.RetagTransparentInputCasts(n.stringIOChain, env.Type)
		if err != nil {
			return nil, false, err
		}
		n.stringIOChain = chain
		changed = changed || chainChanged
	}
	if n.unknown != nil {
		if err := checkChildren("type inquiry", children, 1); err != nil {
			return nil, false, err
		}
		castType := n.unknown.castType
		if castType != nil {
			castType = env.Type(castType)
		}
		if children[0].ResultUsage().Type != env.Type(n.Args[0].ResultUsage().Type) {
			return nil, false, fmt.Errorf("stuntgres: invalid retagged unknown type inquiry")
		}
		type sourceCarrier interface{ UnknownInputSource() *expr.UnknownInput }
		acceptedCarrier, acceptedOK := n.unknown.transport.(sourceCarrier)
		candidateCarrier, candidateOK := children[0].(sourceCarrier)
		if !acceptedOK || !candidateOK {
			return nil, false, fmt.Errorf("stuntgres: missing retagged unknown type inquiry source")
		}
		accepted, candidate := acceptedCarrier.UnknownInputSource(), candidateCarrier.UnknownInputSource()
		if accepted == nil || candidate == nil || candidate.CastType != castType ||
			!reflect.DeepEqual(candidate.Literal, accepted.Literal) ||
			!reflect.DeepEqual(candidate.Source, accepted.Source) || len(candidate.Chain) != len(accepted.Chain) {
			return nil, false, fmt.Errorf("stuntgres: invalid retagged unknown type inquiry provenance")
		}
		chainChanged := false
		for i, step := range accepted.Chain {
			target := env.Type(step.Target)
			if !expr.TransparentUnknownTarget(target) || target.ID() != step.Target.ID() ||
				candidate.Chain[i].Target != target || !reflect.DeepEqual(candidate.Chain[i].Syntax, step.Syntax) {
				return nil, false, fmt.Errorf("stuntgres: invalid retagged unknown type inquiry chain")
			}
			chainChanged = chainChanged || target != step.Target
		}
		if castType != n.unknown.castType || chainChanged || !reflect.DeepEqual(children[0], n.unknown.transport) {
			witness := *n.unknown
			witness.castType, witness.transport = castType, children[0]
			n.unknown = &witness
			changed = true
		}
	}
	node, err := n.Rebuild(info, children)
	return node, changed, err
}

func (TypeInquiry) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (TypeInquiry) PlanningFold() expr.Fold              { return expr.FoldNever }
func (TypeInquiry) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (TypeInquiry) LooksLikeFunction() bool              { return true }
func (TypeInquiry) CallCost() int                        { return 1 }
func (n TypeInquiry) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return false, false, n.Kind != TypeofInquiry
}
func (n TypeInquiry) ClosedDefault(closed func(expr.Expr) bool) bool {
	for _, arg := range n.Args {
		if !closed(arg) {
			return false
		}
	}
	return true
}
func (n TypeInquiry) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(TypeInquiry)
	if !ok || n.Kind != o.Kind || n.logical != o.logical || n.result != o.result || n.routine != o.routine || n.stringIO != o.stringIO || (n.unknown == nil) != (o.unknown == nil) {
		return false
	}
	if n.unknown != nil && (n.unknown.raw.Value.Kind != o.unknown.raw.Value.Kind || n.unknown.raw.Value.Text != o.unknown.raw.Value.Text || n.unknown.castType != o.unknown.castType) {
		return false
	}
	return sameChildren(n.Args, o.Args, same)
}
func (n TypeInquiry) name() string {
	switch n.Kind {
	case FormatTypeInquiry:
		return "format_type"
	case BaseTypeInquiry:
		return "pg_basetype"
	default:
		return "pg_typeof"
	}
}
func (n TypeInquiry) Validate() error {
	if n.result == nil || n.Usage.Type != n.result || n.routine == nil || n.RoutineReference != n.routine {
		return fmt.Errorf("stuntgres: invalid stored type inquiry generation")
	}
	routineID := uint32(1619)
	if n.Kind == FormatTypeInquiry {
		routineID = 1081
	} else if n.Kind == BaseTypeInquiry {
		routineID = 6315
	}
	if n.routine.RoutineOID() != routineID {
		return fmt.Errorf("stuntgres: invalid stored type inquiry provider")
	}
	arity, result := 1, sqltype.Regtype
	formals := [2]sqltype.ID{sqltype.Regtype, 0}
	switch n.Kind {
	case FormatTypeInquiry:
		arity, result = 2, sqltype.Text
		formals = [2]sqltype.ID{sqltype.Oid, sqltype.Int4}
	case BaseTypeInquiry, TypeofInquiry:
	default:
		return fmt.Errorf("stuntgres: invalid stored type inquiry kind")
	}
	u := n.ResultUsage()
	if len(n.Args) != arity || u.Type == nil || u != u.Type.Usage() || u.Type.ID() != result {
		return fmt.Errorf("stuntgres: invalid stored type inquiry signature")
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: invalid stored type inquiry argument")
		}
		if n.Kind != TypeofInquiry && (arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != formals[i]) {
			return fmt.Errorf("stuntgres: invalid stored type inquiry formal")
		}
		if n.Kind != TypeofInquiry && arg.ResultUsage().Type != n.formals[i] {
			return fmt.Errorf("stuntgres: invalid stored type inquiry formal generation")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored type inquiry properties")
	}
	if n.Kind != TypeofInquiry {
		if n.logical != 0 || n.unknown != nil || n.stringIO != nil {
			return fmt.Errorf("stuntgres: invalid stored type inquiry witness")
		}
	} else if n.stringIO != nil {
		if n.unknown != nil || n.stringIO.ID() != sqltype.Unknown || n.logical != sqltype.Unknown {
			return fmt.Errorf("stuntgres: invalid stored unknown IO identity")
		}
		carrier, ok := n.Args[0].(expr.InputCaster)
		if !ok {
			return fmt.Errorf("stuntgres: missing stored unknown IO transport")
		}
		cast, err := carrier.LogicalInputCast()
		if err != nil {
			return err
		}
		if cast.Target.Type != n.stringIO || cast.Target != n.stringIO.Usage() || cast.Source == nil {
			return fmt.Errorf("stuntgres: invalid stored unknown IO target generation")
		}
		if !reflect.DeepEqual(cast.Syntax, n.stringIOSyntax) || !expr.SameTransparentInputCasts(cast.Transparent, n.stringIOChain) {
			return fmt.Errorf("stuntgres: changed stored unknown IO cast provenance")
		}
	} else if n.unknown != nil {
		kind := n.unknown.raw.Value.Kind
		if n.logical != sqltype.Unknown || (kind != parser.StringValue && kind != parser.NullValue) || n.Args[0].ResultUsage().Type.ID() != sqltype.Text || !reflect.DeepEqual(n.Args[0], n.unknown.transport) {
			return fmt.Errorf("stuntgres: invalid stored unknown type inquiry witness")
		}
		carrier, ok := n.Args[0].(interface{ UnknownInputSource() *expr.UnknownInput })
		if !ok {
			return fmt.Errorf("stuntgres: missing stored unknown type inquiry source")
		}
		source := carrier.UnknownInputSource()
		if source == nil || !reflect.DeepEqual(source.Literal, n.unknown.raw) || !reflect.DeepEqual(source.Source, n.unknown.source) || source.CastType != n.unknown.castType {
			return fmt.Errorf("stuntgres: changed stored unknown type inquiry provenance")
		}
		if err := source.ValidateProvenance(); err != nil {
			return err
		}
	} else if n.logical != n.Args[0].ResultUsage().Type.ID() || n.logical == sqltype.Unknown || n.logical == 0 {
		return fmt.Errorf("stuntgres: invalid stored logical type inquiry witness")
	}
	return nil
}

func (n TypeInquiry) ValidateStoredProvider() error { return n.Validate() }

func (n TypeInquiry) unknownSQL() string {
	literal := "NULL"
	if n.unknown.raw.Value.Kind != parser.NullValue {
		text := strings.ReplaceAll(n.unknown.raw.Value.Text, "'", "''")
		if strings.Contains(text, "\\") {
			literal = "E'" + strings.ReplaceAll(text, "\\", "\\\\") + "'"
		} else {
			literal = "'" + text + "'"
		}
	}
	if n.unknown.castType != nil {
		return "CAST(" + literal + " AS pg_catalog.\"" + strings.ReplaceAll(n.unknown.castType.Name(), "\"", "\"\"") + "\")"
	}
	return literal
}
func (n TypeInquiry) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg, Transport: expr.TransportAmbiguousText}
	}
	r := expr.Rendering{Tree: fmt.Sprintf("TypeInquiry %d", n.Kind), Inputs: n.Args, Form: expr.FormCall, Name: n.name(), Args: args, Replay: expr.Replay{Validate: n.Validate, AlwaysVisibilitySnapshot: n.Kind != TypeofInquiry}}
	if n.unknown != nil {
		// Display and replay preserve original logical UNKNOWN syntax, rather
		// than introducing a cast from the concrete executable TEXT transport.
		r.Form = expr.FormTemplate
		r.Pieces = []expr.Piece{expr.TextPiece("pg_catalog.pg_typeof(" + n.unknownSQL() + ")")}
		r.Replay.Pieces = r.Pieces
		if n.unknown.castType != nil {
			source := n.unknown.transport.(interface{ UnknownInputSource() *expr.UnknownInput }).UnknownInputSource()
			if len(source.Chain) == 0 {
				r.Replay.Types = []*sqltype.Descriptor{n.unknown.castType}
			} else {
				r.Replay.Types = make([]*sqltype.Descriptor, len(source.Chain))
				for i, step := range source.Chain {
					r.Replay.Types[i] = step.Target
				}
			}
		}
	}
	return r, nil
}
func (n TypeInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: len(n.Args), Args: n.Args}, true, nil
}
