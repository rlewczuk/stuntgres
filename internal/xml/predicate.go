package xml

import (
	"context"
	"errors"
	"fmt"
)

func IsWellFormed(ctx context.Context, input string, mode Mode) (bool, error) {
	_, err := Parse(ctx, input, Options{Mode: mode})
	var parseError *ParseError
	if errors.As(err, &parseError) {
		return false, nil
	}
	return err == nil, err
}
func IsDocument(ctx context.Context, value Result) (bool, error) {
	if !value.Valid() {
		return false, fmt.Errorf("invalid XML value")
	}
	return IsWellFormed(ctx, value.original, Document)
}

// XMLExists evaluates the entire demanded XPath result. Scalars each have
// cardinality one, regardless of their XPath boolean conversion.
func XMLExists(ctx context.Context, path string, value Result) (bool, []Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	if !value.Valid() {
		return false, nil, fmt.Errorf("invalid XML value")
	}
	if path == "" {
		return false, nil, &XPathError{Stage: "empty"}
	}
	frame, events, err := documentFrame(ctx, value)
	if err != nil {
		return false, events, err
	}
	expression, err := compileXPath(ctx, path)
	if err != nil {
		return false, events, err
	}
	result := frame.eval(expression)
	if frame.p.err != nil {
		return false, events, frame.p.err
	}
	return result.kind != xpathNodes || len(result.nodes) != 0, events, nil
}

type XPathContextError struct{ Parse *ParseError }

func (*XPathContextError) Error() string   { return "could not parse XML document" }
func (e *XPathContextError) Unwrap() error { return e.Parse }
