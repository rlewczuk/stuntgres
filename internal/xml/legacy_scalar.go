package xml

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// LegacyScalarKind selects one xml2 conversion, not a core XML operation.
type LegacyScalarKind uint8

const (
	LegacyString LegacyScalarKind = iota
	LegacyNumber
	LegacyBoolean
)

type LegacyScalar struct {
	Null    bool
	Text    string
	Number  float32
	Boolean bool
}

// LegacyXPathError carries the invocation's complete ordered diagnostic text.
// It is intentionally distinct from the core XPath error policy.
type LegacyXPathError struct{ Detail string }

func (e *LegacyXPathError) Error() string { return "XPath Syntax Error" }

func legacyDocumentFailure(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pe *ParseError
	if errors.As(err, &pe) {
		return true
	}
	var raw *RawInputError
	if errors.As(err, &raw) {
		switch raw.Kind {
		case RawUnknownLabel, RawUnavailableCodec, RawInvalidSequence, RawEmptyInput:
			return true
		}
	}
	return false
}

// XPathScalar parses raw TEXT bytes once, builds an invocation-private tree and
// starts at its real document element. No parse tree or decoder escapes the call.
func XPathScalar(ctx context.Context, input, path string, kind LegacyScalarKind) (LegacyScalar, error) {
	failure := LegacyScalar{Null: kind != LegacyBoolean}
	if err := ctx.Err(); err != nil {
		return LegacyScalar{}, err
	}
	if kind > LegacyBoolean {
		return LegacyScalar{}, fmt.Errorf("invalid legacy XPath scalar kind")
	}
	_, document, events, err := parseConfigured(ctx, input, Options{Mode: Document, InputKind: RawMemory}, parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, documentContext: true, legacyDiagnostics: true})
	if err != nil {
		if legacyDocumentFailure(err) {
			if err := ctx.Err(); err != nil {
				return LegacyScalar{}, err
			}
			return failure, nil
		}
		return LegacyScalar{}, err
	}
	if document == nil {
		return LegacyScalar{}, fmt.Errorf("missing legacy XPath document")
	}
	frame, err := newXPathTree(ctx, document)
	if err != nil {
		return LegacyScalar{}, err
	}
	frame.node = nil
	for _, child := range frame.tree.root.children {
		if !frame.poll() {
			return LegacyScalar{}, frame.p.err
		}
		if child.kind == xpathElement {
			frame.node = child
			break
		}
	}
	if frame.node == nil {
		return LegacyScalar{}, fmt.Errorf("missing legacy XPath document element")
	}
	if kind == LegacyString {
		p := parser{ctx: ctx}
		path = p.joinMessage("string(", path, ")")
		if p.err != nil {
			return LegacyScalar{}, p.err
		}
	}
	compiled, err := compileXPath(ctx, path)
	if err != nil {
		var xp *XPathError
		if !errors.As(err, &xp) || xp.Stage != "compile" {
			return LegacyScalar{}, err
		}
		p := parser{ctx: ctx}
		if !appendSize(len(events), len(events)) || !appendSize(len(events)*2, 1) {
			return LegacyScalar{}, appendSizeError(len(events), len(events))
		}
		parts := make([]string, 0, len(events)*2+1)
		for _, event := range events {
			if err := ctx.Err(); err != nil {
				return LegacyScalar{}, err
			}
			parts = append(parts, event.String(), "\n")
		}
		parts = append(parts, xp.Detail)
		detail := p.joinMessage(parts...)
		if p.err != nil {
			return LegacyScalar{}, p.err
		}
		return LegacyScalar{}, &LegacyXPathError{Detail: detail}
	}
	value := frame.eval(compiled)
	if frame.p.err != nil {
		var xp *XPathError
		if errors.As(frame.p.err, &xp) && xp.Stage == "evaluate" {
			if err := ctx.Err(); err != nil {
				return LegacyScalar{}, err
			}
			return failure, nil
		}
		return LegacyScalar{}, frame.p.err
	}
	var result LegacyScalar
	switch kind {
	case LegacyString:
		result.Text = frame.string(value)
	case LegacyNumber:
		number := frame.number(value)
		result.Null = math.IsNaN(number)
		if !result.Null {
			result.Number = float32(number)
		}
	case LegacyBoolean:
		result.Boolean = frame.boolean(value)
	}
	if frame.p.err != nil {
		return LegacyScalar{}, frame.p.err
	}
	if err := ctx.Err(); err != nil {
		return LegacyScalar{}, err
	}
	return result, nil
}

// ValidateText is the warning-bearing Text/current-mode bridge. Unlike legacy
// XPath, it uses the ordinary validator policy and never collects a tree.
func ValidateText(ctx context.Context, input string, mode Mode) (bool, []Diagnostic, error) {
	_, _, events, err := parseConfigured(ctx, input, Options{Mode: mode, InputKind: Text}, parsePolicy{})
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, events, err
	}
	var pe *ParseError
	if errors.As(err, &pe) {
		if err := ctx.Err(); err != nil {
			return false, events, err
		}
		return false, events, nil
	}
	return err == nil, events, err
}
