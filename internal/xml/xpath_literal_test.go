package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestXPathLiteralDiagnostics(t *testing.T) {
	for _, path := range []string{`'`, `"`, `'open`, `"open`, `concat('a`, `concat("a`} {
		t.Run(path, func(t *testing.T) {
			_, err := compileXPath(context.Background(), path)
			var diagnostic *XPathError
			if !errors.As(err, &diagnostic) || diagnostic.Stage != "compile" || diagnostic.Detail != "Unfinished literal" {
				t.Fatal("quoted literal EOF lost its owning scanner diagnostic", err)
			}
		})
	}
	for _, path := range []string{`'closed'`, `"closed"`, `concat('a',"b")`} {
		if _, err := compileXPath(context.Background(), path); err != nil {
			t.Fatal("complete quoted literal", path, err)
		}
	}
	for _, path := range []string{`[`, `concat('a',)`} {
		_, err := compileXPath(context.Background(), path)
		var diagnostic *XPathError
		if !errors.As(err, &diagnostic) || diagnostic.Stage != "compile" || diagnostic.Detail != "Invalid expression" {
			t.Fatal("nonliteral grammar error was reclassified", path, err)
		}
	}
}

func TestXPathLiteralCancellation(t *testing.T) {
	// next's entry poll admits the actual scanner; its second poll is inside
	// the long unterminated literal, before EOF diagnostic classification.
	ctx := &phaseContext{Context: context.Background(), phase: "next", after: 2, cause: context.Canceled}
	_, err := compileXPath(ctx, "'"+strings.Repeat("x", 8192))
	var diagnostic *XPathError
	if !ctx.admitted || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
		t.Fatal("literal scanner replaced ordinary cancellation", ctx.admitted, err)
	}
}
