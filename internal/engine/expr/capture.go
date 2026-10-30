package expr

import "fmt"

// CaptureKind identifies written syntax, not execution or rendering order.
type CaptureKind uint8

const (
	CaptureFunction CaptureKind = iota + 1
	CaptureOperator
	CaptureSQLJSON
	CaptureJSONPredicate
	CaptureCurrentDateTime
	CaptureTimeZone
	CaptureLocal
	CaptureField
	CaptureIdentity
	CaptureXMLParse
	CaptureXMLSerialize
	CaptureXMLDocument
)

// Capturable explicitly admits a raw syntax kind. It must not evaluate, render,
// rebuild, rebind or mutate the node. Engine wrappers take precedence over it.
type Capturable interface {
	Capture(CaptureKind) (CaptureOperands, bool, error)
}

// IndexedCapturable reads written operands directly from the original boxed node.
// It is storage access only: Capturable still owns syntax admission.
type IndexedCapturable interface {
	CaptureOperand(written int) (Expr, error)
}

// CaptureOperands borrows safe slice storage, uses two inline operands, or indexes
// the already-boxed source. Never borrow arrays from a value receiver copy.
// Count is explicit, including zero. Annotations request engine-owned capture;
// they carry no catalog/coercion logic.
type CaptureOperands struct {
	Args             []Expr
	Count            int
	Inline           [2]Expr
	Indexed          bool
	SelectedImplicit bool
	CallImplicit     bool
	PosixSubstring   bool
	SimilarSubstring bool
	JSONSources      bool
	XMLSources       bool
	DotBuiltin       string
}

// At checks written and storage bounds without boxing another source value.
func (o CaptureOperands) At(source Capturable, written int) (Expr, error) {
	if written < 0 || written >= o.Count {
		return nil, fmt.Errorf("invalid capture operand count")
	}
	var arg Expr
	if o.Indexed {
		indexed, ok := source.(IndexedCapturable)
		if !ok || o.Args != nil || o.Inline[0] != nil || o.Inline[1] != nil {
			return nil, fmt.Errorf("invalid indexed capture storage")
		}
		var err error
		arg, err = indexed.CaptureOperand(written)
		if err != nil {
			return nil, err
		}
	} else if o.Args != nil {
		if written >= len(o.Args) {
			return nil, fmt.Errorf("invalid capture operand slot")
		}
		arg = o.Args[written]
	} else {
		if written >= len(o.Inline) {
			return nil, fmt.Errorf("invalid inline capture operand")
		}
		arg = o.Inline[written]
	}
	if arg == nil {
		return nil, fmt.Errorf("nil written capture operand")
	}
	return arg, nil
}

// Check validates every operand before the engine writes annotations or walks.
func (o CaptureOperands) Check(source Capturable, count int) error {
	if o.Count != count || count < 0 {
		return fmt.Errorf("mismatched view literal arguments")
	}
	if o.Indexed {
		if _, ok := source.(IndexedCapturable); !ok || o.Args != nil || o.Inline[0] != nil || o.Inline[1] != nil {
			return fmt.Errorf("invalid indexed capture storage")
		}
	}
	for i := range count {
		if _, err := o.At(source, i); err != nil {
			return err
		}
	}
	return nil
}
