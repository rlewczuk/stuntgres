// Package xml validates XML character values without retaining a document tree.
package xml

import (
	"context"
	"fmt"
)

type Mode uint8

const (
	Content Mode = iota
	Document
)

type InputKind uint8

const (
	Text InputKind = iota
	Binary
)

type Options struct {
	Mode      Mode
	InputKind InputKind
}
type Standalone uint8

const (
	Absent Standalone = iota
	No
	Yes
)

type Declaration struct {
	Present         bool
	Version         string
	Encoding        string
	EncodingPresent bool
	Standalone      Standalone
	End             int
}

// Result contains only immutable character spelling and small parsing metadata.
// A zero Result is not an accepted empty CONTENT value.
type Result struct {
	original             string
	requested, effective Mode
	declaration          Declaration
	diagnostics          []Diagnostic
	valid                bool
}

func (r Result) Original() string          { return r.original }
func (r Result) RequestedMode() Mode       { return r.requested }
func (r Result) EffectiveMode() Mode       { return r.effective }
func (r Result) Declaration() Declaration  { return r.declaration }
func (r Result) Diagnostics() []Diagnostic { return append([]Diagnostic(nil), r.diagnostics...) }

// Valid reports genuine successful parser acceptance, including empty CONTENT.
func (r Result) Valid() bool { return r.valid }

func Parse(ctx context.Context, input string, opts Options) (Result, error) {
	r, _, err := parse(ctx, input, opts, false)
	return r, err
}
func parse(ctx context.Context, input string, opts Options, collect bool) (Result, *document, error) {
	r, d, _, err := parseConfigured(ctx, input, opts, collect, false)
	return r, d, err
}
func parseConfigured(ctx context.Context, input string, opts Options, collect, ignoreBlanks bool) (Result, *document, []Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, nil, nil, err
	}
	if opts.Mode > Document || opts.InputKind > Binary {
		return Result{}, nil, nil, fmt.Errorf("invalid XML options")
	}
	if opts.InputKind == Binary {
		d, _, err := readContentDeclaration(ctx, input)
		if err != nil {
			return Result{}, nil, nil, err
		}
		id := 6
		if d.EncodingPresent {
			id = encodingID(d.Encoding)
			if id < 0 {
				messageParser := parser{ctx: ctx}
				message := messageParser.joinMessage("invalid encoding name \"", d.Encoding, "\"")
				if messageParser.err != nil {
					return Result{}, nil, nil, messageParser.err
				}
				return Result{}, nil, nil, &EncodingError{Kind: InvalidEncodingName, Label: d.Encoding, SourceID: -1, DestinationID: 6, DestinationName: "UTF8", Message: message}
			}
		}
		var errConvert error
		input, errConvert = convert(ctx, input, id, 6)
		if errConvert != nil {
			return Result{}, nil, nil, errConvert
		}
	}
	p := parser{ctx: ctx, s: input, requested: opts.Mode, effective: opts.Mode, line: 1, column: 1, replaceEntities: opts.Mode == Document, ignoreBlanks: ignoreBlanks, spaceState: -1}
	if collect {
		p.tree = &document{}
	}
	if opts.Mode == Content {
		d, detail, err := readContentDeclaration(ctx, input)
		if err != nil {
			return Result{}, nil, p.events, err
		}
		p.declaration = d
		if detail != "" {
			return Result{}, nil, p.events, &ParseError{Category: DeclarationError, RequestedMode: opts.Mode, EffectiveMode: opts.Mode, Declaration: d, DeclarationDetail: detail}
		}
		p.base, p.i = d.End, d.End
		p.fragmentInput = true
		p.windowBase = d.End
		if p.leadingDoctype(d.End) {
			p.effective = Document
			p.base = 0
			p.i = 0
			p.fragmentInput = false
			p.windowBase = 0
			p.replaceEntities = true
			p.documentDeclaration()
		}
	} else {
		p.documentDeclaration()
	}
	if p.err == nil {
		p.top()
	}
	if p.err != nil {
		return Result{}, nil, p.events, p.err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, nil, p.events, err
	}
	if p.failed() {
		return Result{}, nil, p.events, p.parseError()
	}
	if collect {
		p.finishDocument()
		if p.err != nil {
			return Result{}, nil, p.events, p.err
		}
		if err := ctx.Err(); err != nil {
			return Result{}, nil, p.events, err
		}
	}
	return Result{original: input, requested: opts.Mode, effective: p.effective, declaration: p.declaration, diagnostics: p.events, valid: true}, p.tree, p.events, nil
}
