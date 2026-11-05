package xml

import (
	"context"
	"errors"
	"strings"
)

type XSLTResult struct {
	Text string
	Null bool
}
type XSLTStage uint8

const (
	XSLTSourceParse XSLTStage = iota
	XSLTStyleParse
	XSLTCompile
	XSLTApply
)

type XSLTError struct {
	Stage  XSLTStage
	Detail string
}

func (e *XSLTError) Error() string {
	switch e.Stage {
	case XSLTSourceParse:
		return "error parsing XML document"
	case XSLTStyleParse:
		return "error parsing stylesheet as XML document"
	case XSLTCompile:
		return "failed to parse stylesheet"
	default:
		return "failed to apply stylesheet"
	}
}

// ProcessXSLT owns both readers, the compiled stylesheet, transformation state
// and serialization. Only a complete immutable datum crosses this boundary.
func ProcessXSLT(ctx context.Context, source, stylesheet, params string) (XSLTResult, error) {
	if err := ctx.Err(); err != nil {
		return XSLTResult{}, err
	}
	pairs, err := xsltSplitParams(ctx, params)
	if err != nil {
		return XSLTResult{}, err
	}
	var diagnostics []Diagnostic
	read := func(input string, stage XSLTStage) (*document, error) {
		_, d, events, err := parseConfigured(ctx, input, Options{Mode: Document, InputKind: RawMemory}, parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, legacyDiagnostics: true, xsltReader: true})
		for _, event := range events {
			p := parser{ctx: ctx}
			diagnostics = checkedAppend(&p, diagnostics, event)
			if p.err != nil {
				return nil, p.err
			}
		}
		if err == nil {
			return d, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var pe *ParseError
		var re *RawInputError
		if errors.As(err, &pe) || errors.As(err, &re) {
			detail, renderError := xsltDiagnosticText(ctx, diagnostics)
			if renderError != nil {
				return nil, renderError
			}
			return nil, &XSLTError{Stage: stage, Detail: detail}
		}
		return nil, err
	}
	src, err := read(source, XSLTSourceParse)
	if err != nil {
		return XSLTResult{}, err
	}
	style, err := read(stylesheet, XSLTStyleParse)
	if err != nil {
		return XSLTResult{}, err
	}
	program, err := compileXSLT(ctx, style)
	if err != nil {
		return XSLTResult{}, xsltStageError(ctx, err, XSLTCompile, diagnostics)
	}
	tree, err := applyXSLT(ctx, program, src, pairs)
	if err != nil {
		return XSLTResult{}, xsltStageError(ctx, err, XSLTApply, diagnostics)
	}
	if err = ctx.Err(); err != nil {
		return XSLTResult{}, err
	}
	return saveXSLT(ctx, tree, program.output)
}
func xsltStageError(ctx context.Context, err error, stage XSLTStage, events []Diagnostic) error {
	var xe *XSLTError
	var xp *XPathError
	var detail string
	switch {
	case errors.As(err, &xe):
		detail = xe.Detail
	case errors.As(err, &xp):
		detail = xp.Detail
	default:
		return err
	}
	prior, renderError := xsltDiagnosticText(ctx, events)
	if renderError != nil {
		return renderError
	}
	p := parser{ctx: ctx}
	if prior != "" && detail != "" {
		detail = p.joinMessage(prior, "\n", detail)
	} else {
		detail = p.joinMessage(prior, detail)
	}
	if p.err != nil {
		return p.err
	}
	return &XSLTError{Stage: stage, Detail: detail}
}

func xsltDiagnosticText(ctx context.Context, events []Diagnostic) (string, error) {
	p := parser{ctx: ctx}
	var b strings.Builder
	for _, d := range events {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if b.Len() != 0 {
			p.builderByte(&b, '\n')
		}
		text := d.Message
		if d.Domain != EncodingDomain && d.Domain != IODomain {
			text = d.String()
		}
		p.appendValue(&b, text)
		if p.err != nil {
			return "", p.err
		}
	}
	return b.String(), ctx.Err()
}
