package sqltype

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

type xpathConversionContext struct {
	context.Context
	admitted, final, canceled bool
	polls                     int
	cause                     error
}

func (c *xpathConversionContext) Err() error {
	if c.canceled {
		return c.cause
	}
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	conversion := false
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "sqltype.XMLInput") {
			conversion = true
		}
		if !more {
			break
		}
	}
	if conversion {
		c.admitted = true
		c.polls++
		if !c.final && c.polls >= 2 {
			c.canceled = true
			return c.cause
		}
	}
	if c.final && c.admitted && !conversion {
		c.canceled = true
		return c.cause
	}
	return nil
}
func TestXMLXPathConversionCancellation(t *testing.T) {
	types := testTypes()
	source, err := types[XML].Input(context.Background(), "<r><a>one</a><a>two</a></r>", -1)
	if err != nil {
		t.Fatal(err)
	}
	backing := source.xml
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, final := range []bool{false, true} {
			ctx := &xpathConversionContext{Context: context.Background(), final: final, cause: cause}
			got, err := XMLXPath(ctx, types[143], "/r/a", source, Value{}, false, InputEnvironment{}, OutputEnvironment{})
			if !ctx.admitted || !errors.Is(err, cause) || got.typ != nil || source.xml != backing {
				t.Fatal("conversion/final-publication cancellation", ctx.admitted, got, err)
			}
			healthy, err := XMLXPath(context.Background(), types[143], "/r/a", source, Value{}, false, InputEnvironment{}, OutputEnvironment{})
			if err != nil || healthy.ArrayLen() != 2 || !healthy.ValidResult(types[143].Usage()) {
				t.Fatal("healthy reuse", healthy, err)
			}
		}
	}
}
